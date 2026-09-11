package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/postgres"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/spf13/cobra"
)

var configFile string

func buildServerRuntime(ctx context.Context, cfg *config.Config, logger *slog.Logger) (_ *serverRuntime, err error) {
	var runtime *serverRuntime
	defer func() {
		if err != nil && runtime != nil {
			err = errors.Join(err, runtime.Close(context.Background()))
		}
	}()
	signingExpiry, err := resolveSigningExpiry(cfg.Signing.DefaultExpirySeconds)
	if err != nil {
		return nil, err
	}

	applyCredentialEncryptionConfig(cfg)
	cipher, cipherErr := credentialcipher.NewFromEnv()
	if cipherErr != nil {
		return nil, fmt.Errorf("invalid credential encryption configuration: %w", cipherErr)
	}

	// Init DB
	var backend serverBackend
	var database *store.Store
	var errDb error

	if cfg.Database.Sqlite != nil {
		dbPath := cfg.Database.Sqlite.File
		if dbPath == "" {
			dbPath = "drs.db"
			cfg.Database.Sqlite.File = dbPath
		}
		logger.Info("initializing sqlite database", "file", dbPath)
		database, errDb = sqlite.NewSqliteDB(dbPath, cipher)
		if errDb == nil {
			backend = serverBackendForStore(database)
		}
	} else if cfg.Database.Postgres != nil {
		dsn := postgresDSN(*cfg.Database.Postgres)
		logger.Info("initializing postgres database", "host", cfg.Database.Postgres.Host, "database", cfg.Database.Postgres.Database)
		database, errDb = postgres.NewPostgresDB(dsn, cipher)
		if errDb == nil {
			backend = serverBackendForStore(database)
		}
	} else {
		return nil, fmt.Errorf("no database configuration provided")
	}

	if errDb != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", errDb)
	}
	runtime = &serverRuntime{database: database}

	needsStorage := cfg.Routes.Ga4gh || cfg.Routes.Internal || cfg.Routes.LFS
	var invalidator *storageInvalidator
	var storageManager *storage.Manager
	if needsStorage {
		invalidator = &storageInvalidator{}
	}
	bucketService, err := buckets.NewService(backend.bucketDependencies, invalidator)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize bucket service: %w", err)
	}
	if needsStorage {
		var storageErr error
		storageManager, storageErr = newStorageManager(bucketService, "/", logger)
		if storageErr != nil {
			return nil, fmt.Errorf("failed to initialize storage manager: %w", storageErr)
		}
		invalidator.manager = storageManager
	}

	// Load configured bucket credentials if present.
	if len(cfg.Buckets) > 0 {
		encryptionEnabled, encErr := cipher.Enabled()
		if encErr != nil {
			return nil, fmt.Errorf("invalid credential encryption configuration for %s: %w", credentialcipher.CredentialMasterKeyEnv, encErr)
		}
		if !encryptionEnabled {
			return nil, fmt.Errorf("s3 credential encryption key is required: %s", credentialcipher.CredentialMasterKeyEnv)
		}

		logger.Info("loading configured bucket credentials", "count", len(cfg.Buckets))
		// Bucket credentials are encrypted before persistence and audited on read/write/delete/list.
		for _, c := range cfg.Buckets {
			cred := &buckets.Credential{
				CredentialID: c.CredentialID,
				Bucket:       c.Bucket,
				Provider:     c.Provider,
				Region:       c.Region,
				AccessKey:    c.AccessKey,
				SecretKey:    c.SecretKey,
				Endpoint:     c.Endpoint,
			}
			if err := bucketService.SaveS3Credential(ctx, cred); err != nil {
				logger.Error("failed to save s3 credential", "bucket", c.Bucket, "err", err)
			}
		}
	}
	if err := loadConfiguredBucketScopes(ctx, bucketService, bucketService, cfg.BucketScopes, logger); err != nil {
		return nil, fmt.Errorf("failed to load configured bucket scopes: %w", err)
	}

	objectService := objects.NewService(backend.objectStore)
	usageService := usage.NewService(usage.Dependencies{
		Reports: backend.usageReports,
		Objects: objectService,
	})
	transferService := transfers.NewService(transfers.Dependencies{
		Objects:              objectService,
		Storage:              storageManager,
		FileCounters:         backend.usageIngest,
		Scopes:               bucketService,
		Credentials:          bucketService,
		Events:               backend.usageIngest,
		DefaultSigningExpiry: signingExpiry,
	})
	lfsService := transferlfs.NewService(transferService, objectService, bucketService, backend.pending, backend.usageIngest, nil)
	projectStorageService := projectstorage.NewService(projectstorage.Dependencies{
		ScopeResolver: bucketService,
		Credentials:   bucketService,
		Visibility:    bucketService,
		Records:       objectService,
		ObjectCleanup: objectService,
		ScopeCatalog:  bucketService,
		Providers:     projectstorage.Providers{Inventory: storageManager, Probe: storageManager, Delete: storageManager},
	})

	// Build the Fiber runtime and request handlers.
	app := fiber.New(fiber.Config{
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   120 * time.Second,
		IdleTimeout:    120 * time.Second,
		ReadBufferSize: 64 * 1024,
		AppName:        "Syfon DRS Server",
		ErrorHandler:   httpapi.FiberErrorHandler,
	})
	app.Use(recover.New())

	// Build the authorization and request ID handlers.
	// We use a standard slog.Logger for data-client compatibility
	slogLogger := logger
	authRuntime := authentication.NewRuntime(slogLogger, cfg.Auth)
	runtime.authRuntime = authRuntime
	authzHandler := httpapi.AuthorizationHandler(httpapi.AuthzOptions{
		Mode:      cfg.Auth.Mode,
		Evaluator: authRuntime,
	})
	requestIDHandler := httpapi.RequestIDHandler(slogLogger)

	runtime.app = app
	runtime.cfg = cfg
	runtime.serviceInfo = serviceInfoForBackend(cfg.Database.Sqlite != nil)
	runtime.objectService = objectService
	runtime.transferService = transferService
	runtime.lfsService = lfsService
	runtime.usageService = usageService
	runtime.usageIngest = backend.usageIngest
	runtime.projectStorage = projectStorageService
	runtime.bucketService = bucketService
	runtime.authzHandler = authzHandler
	runtime.requestIDHandler = requestIDHandler
	return runtime, nil
}

const maxSigningExpirySeconds = int64((1<<63 - 1) / int64(time.Second))

func resolveSigningExpiry(seconds int) (time.Duration, error) {
	if seconds <= 0 {
		return time.Duration(config.DefaultSigningExpirySeconds) * time.Second, nil
	}
	if uint64(seconds) > uint64(maxSigningExpirySeconds) {
		return 0, fmt.Errorf("signing default expiry seconds exceed the maximum duration")
	}
	return time.Duration(seconds) * time.Second, nil
}

func postgresDSN(cfg config.PostgresConfig) string {
	query := url.Values{"sslmode": {cfg.SSLMode}}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: query.Encode(),
	}).String()
}

var Cmd = &cobra.Command{
	Use:     "serve",
	Aliases: []string{"run"},
	Short:   "Starts the DRS Object API server",
	RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		slog.SetDefault(logger)

		// Load Config
		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if cfg.Auth.Mode == config.AuthModeGen3 && cfg.Database.Postgres == nil && !cfg.Auth.Mock.Enabled {
			return fmt.Errorf("auth.mode=gen3 requires postgres database")
		}

		rt, err := buildServerRuntime(cmd.Context(), cfg, logger)
		if err != nil {
			return err
		}
		shutdownRequested := false
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cleanupErr := rt.Close(shutdownCtx)
			if cleanupErr != nil {
				cleanupErr = fmt.Errorf("server shutdown failed: %w", cleanupErr)
			}
			runErr = errors.Join(runErr, cleanupErr)
			if shutdownRequested && cleanupErr == nil {
				logger.Info("server shutdown complete")
			}
		}()
		registerServerRoutes(rt)

		addr := fmt.Sprintf(":%d", cfg.Port)
		logger.Info("server starting", "addr", addr)

		listener, err := net.Listen("tcp4", addr)
		if err != nil {
			return fmt.Errorf("server listen failed: %w", err)
		}
		readyCh := make(chan struct{})
		rt.listener = &firstAcceptListener{Listener: listener, ready: readyCh}

		errCh := make(chan error, 1)
		go func() {
			if err := rt.app.Listener(rt.listener); err != nil {
				errCh <- err
			}
		}()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case <-readyCh:
			// Fiber has entered its serving loop; shutdown can now be selected.
		case err := <-errCh:
			return fmt.Errorf("server listen failed: %w", err)
		}

		select {
		case err := <-errCh:
			return fmt.Errorf("server listen failed: %w", err)
		case sig := <-sigCh:
			logger.Info("shutdown signal received", "signal", sig.String())
			shutdownRequested = true
		case <-cmd.Context().Done():
			logger.Info("shutdown requested by context cancellation")
			shutdownRequested = true
		}

		return nil
	},
}

func applyCredentialEncryptionConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(os.Getenv(credentialcipher.CredentialMasterKeyEnv)) == "" {
		if masterKey := strings.TrimSpace(cfg.CredentialEncryption.MasterKey); masterKey != "" {
			os.Setenv(credentialcipher.CredentialMasterKeyEnv, masterKey)
		}
	}
	if strings.TrimSpace(os.Getenv(credentialcipher.CredentialLocalKeyFileEnv)) == "" {
		if localKeyFile := strings.TrimSpace(cfg.CredentialEncryption.LocalKeyFile); localKeyFile != "" {
			os.Setenv(credentialcipher.CredentialLocalKeyFileEnv, localKeyFile)
		}
	}
	if strings.TrimSpace(os.Getenv(credentialcipher.DatabaseSQLiteFileEnv)) == "" && cfg.Database.Sqlite != nil {
		if sqliteFile := strings.TrimSpace(cfg.Database.Sqlite.File); sqliteFile != "" {
			os.Setenv(credentialcipher.DatabaseSQLiteFileEnv, sqliteFile)
		}
	}
}

func init() {
	Cmd.Flags().StringVar(&configFile, "config", "", "Path to configuration file (json/yaml)")
}
