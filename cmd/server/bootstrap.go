package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

func buildServerRuntime(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*serverRuntime, error) {
	applyCredentialEncryptionConfig(cfg)
	cipher, cipherErr := credentialcipher.NewFromEnv()
	if cipherErr != nil {
		return nil, fmt.Errorf("invalid credential encryption configuration: %w", cipherErr)
	}

	// Init DB
	var backend serverBackend
	var errDb error

	if cfg.Database.Sqlite != nil {
		dbPath := cfg.Database.Sqlite.File
		if dbPath == "" {
			dbPath = "drs.db"
			cfg.Database.Sqlite.File = dbPath
		}
		logger.Info("initializing sqlite database", "file", dbPath)
		var database *store.Store
		database, errDb = sqlite.NewSqliteDB(dbPath, cipher)
		if errDb == nil {
			backend = serverBackendForStore(database)
		}
	} else if cfg.Database.Postgres != nil {
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			cfg.Database.Postgres.User,
			cfg.Database.Postgres.Password,
			cfg.Database.Postgres.Host,
			cfg.Database.Postgres.Port,
			cfg.Database.Postgres.Database,
			cfg.Database.Postgres.SSLMode,
		)
		logger.Info("initializing postgres database", "host", cfg.Database.Postgres.Host, "database", cfg.Database.Postgres.Database)
		var database *store.Store
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
		Objects:      objectService,
		Storage:      storageManager,
		FileCounters: backend.usageIngest,
		Scopes:       bucketService,
		Credentials:  bucketService,
		Events:       backend.usageIngest,
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
	authzHandler := httpapi.AuthorizationHandler(httpapi.AuthzOptions{
		Mode:      cfg.Auth.Mode,
		Evaluator: authRuntime,
	})
	requestIDHandler := httpapi.RequestIDHandler(slogLogger)

	return &serverRuntime{
		app:              app,
		cfg:              cfg,
		serviceInfo:      serviceInfoForBackend(cfg.Database.Sqlite != nil),
		objectService:    objectService,
		transferService:  transferService,
		lfsService:       lfsService,
		usageService:     usageService,
		usageIngest:      backend.usageIngest,
		projectStorage:   projectStorageService,
		bucketService:    bucketService,
		authzHandler:     authzHandler,
		requestIDHandler: requestIDHandler,
	}, nil
}

var Cmd = &cobra.Command{
	Use:     "serve",
	Aliases: []string{"run"},
	Short:   "Starts the DRS Object API server",
	RunE: func(cmd *cobra.Command, args []string) error {
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
		registerServerRoutes(rt)

		addr := fmt.Sprintf(":%d", cfg.Port)
		logger.Info("server starting", "addr", addr)

		errCh := make(chan error, 1)
		go func() {
			if err := rt.app.Listen(addr); err != nil {
				errCh <- err
			}
		}()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case err := <-errCh:
			return fmt.Errorf("server listen failed: %w", err)
		case sig := <-sigCh:
			logger.Info("shutdown signal received", "signal", sig.String())
		case <-cmd.Context().Done():
			logger.Info("shutdown requested by context cancellation")
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := rt.app.ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		logger.Info("server shutdown complete")
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
