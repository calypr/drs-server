package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/spf13/cobra"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/credentialcipher"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/maintenance/projectstorage"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/postgres"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

var configFile string

func serviceInfoForBackend(sqlite bool) drs.Service {
	description := "Calypr-backed DRS server"
	if sqlite {
		description += " (SQLite)"
	}
	createdAt := time.Now()
	updatedAt := time.Now()
	environment := "prod"
	return drs.Service{
		Id:          "drs-service-calypr",
		Name:        "Calypr DRS Server",
		Type:        drs.ServiceType{Group: "org.ga4gh", Artifact: "drs", Version: "1.2.0"},
		Description: &description,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
		Environment: &environment,
		Version:     "1.0.0",
	}
}

type serverBackend struct {
	objectDependencies objects.Dependencies
	bucketDependencies buckets.Dependencies
	pending            transfers.PendingStore
	usageIngest        usage.Ingestor
	usageReports       usage.ReportStore
}

var (
	errBucketVisibilityScopeQuery   = errors.New("bucket visibility fallback requires an object scope query")
	errBucketVisibilityRecordReader = errors.New("bucket visibility fallback requires an object record reader")
)

func newBucketVisibilityFallback(scope objects.ScopeQuery, reader objects.RecordReader) buckets.VisibilityFallback {
	return func(ctx context.Context) ([]buckets.VisibilityRow, error) {
		if scope == nil {
			return nil, errBucketVisibilityScopeQuery
		}
		if reader == nil {
			return nil, errBucketVisibilityRecordReader
		}

		ids, err := scope.ListObjectIDsByScope(ctx, "", "")
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []buckets.VisibilityRow{}, nil
		}

		records, err := reader.GetBulkObjects(ctx, ids)
		if err != nil {
			return nil, err
		}
		rows := make([]buckets.VisibilityRow, 0)
		for i := range records {
			obj := &records[i]
			if !serverBucketVisibilityObjectReadable(ctx, obj) {
				continue
			}
			resources := objects.AccessResources(obj)
			if len(resources) == 0 || obj.AccessMethods == nil {
				continue
			}
			for _, method := range *obj.AccessMethods {
				if method.AccessUrl == nil {
					continue
				}
				accessURL := strings.TrimSpace(method.AccessUrl.Url)
				if accessURL == "" {
					continue
				}
				for _, resource := range resources {
					resource = strings.TrimSpace(resource)
					if resource == "" {
						continue
					}
					rows = append(rows, buckets.VisibilityRow{
						AccessURL:  accessURL,
						AccessType: strings.TrimSpace(method.Type),
						Resource:   resource,
					})
				}
			}
		}
		return rows, nil
	}
}

func serverBucketVisibilityObjectReadable(ctx context.Context, obj *objects.Record) bool {
	if !access.IsAuthzEnforced(ctx) ||
		access.HasMethodAccess(ctx, "read", []string{"/programs"}) ||
		access.HasMethodAccess(ctx, "read", []string{"/data_file"}) {
		return true
	}
	if obj != nil && obj.PublicRead {
		return true
	}
	resources := objects.AccessResources(obj)
	if obj != nil && obj.PublicReadPolicyKnown && len(resources) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, "read", resources)
}

func sqliteServerBackend(database *sqlite.SqliteDB) serverBackend {
	return serverBackend{
		objectDependencies: objects.Dependencies{
			Reader: database, Writer: database, AccessMethods: database, AccessPolicy: database,
			Aliases: database, Content: database, ChecksumScope: database, Scope: database,
			Resources: database, Pages: database, URLPages: database, Authorized: database,
		},
		bucketDependencies: buckets.Dependencies{
			Credentials: database, CredentialAdmin: database, Scopes: database, Visibility: database,
		},
		pending:      database,
		usageIngest:  database,
		usageReports: database,
	}
}

func postgresServerBackend(database *postgres.PostgresDB) serverBackend {
	return serverBackend{
		objectDependencies: objects.Dependencies{
			Reader: database, Writer: database, AccessMethods: database, AccessPolicy: database,
			Aliases: database, Content: database, ChecksumScope: database, Scope: database,
			Resources: database, Pages: database, URLPages: database, Authorized: database,
		},
		bucketDependencies: buckets.Dependencies{
			Credentials: database, CredentialAdmin: database, Scopes: database, Visibility: database,
		},
		pending:      database,
		usageIngest:  database,
		usageReports: database,
	}
}

var Cmd = &cobra.Command{
	Use:     "serve",
	Aliases: []string{"run"},
	Short:   "Starts the DRS Object API server",
	Run: func(cmd *cobra.Command, args []string) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		slog.SetDefault(logger)
		fatal := func(msg string, args ...any) {
			logger.Error(msg, args...)
			os.Exit(1)
		}

		// Load Config
		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			fatal("failed to load config", "err", err)
		}
		if cfg.Auth.Mode == config.AuthModeGen3 && cfg.Database.Postgres == nil && !isMockAuthEnabled() {
			fatal("auth.mode=gen3 requires postgres database")
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
			var database *sqlite.SqliteDB
			database, errDb = sqlite.NewSqliteDB(dbPath)
			if errDb == nil {
				backend = sqliteServerBackend(database)
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
			var database *postgres.PostgresDB
			database, errDb = postgres.NewPostgresDB(dsn)
			if errDb == nil {
				backend = postgresServerBackend(database)
			}
		} else {
			fatal("no database configuration provided")
		}

		if errDb != nil {
			fatal("failed to initialize database", "err", errDb)
		}

		applyCredentialEncryptionConfig(cfg)

		needsStorage := cfg.Routes.Ga4gh || cfg.Routes.Internal || cfg.Routes.LFS
		var invalidator *storageInvalidator
		var storageManager *storage.Manager
		if needsStorage {
			invalidator = &storageInvalidator{}
		}
		bucketDependencies := backend.bucketDependencies
		bucketDependencies.Fallback = newBucketVisibilityFallback(
			backend.objectDependencies.Scope,
			backend.objectDependencies.Reader,
		)
		bucketService, err := buckets.NewService(bucketDependencies, invalidator)
		if err != nil {
			fatal("failed to initialize bucket service", "err", err)
		}
		if needsStorage {
			var storageErr error
			storageManager, storageErr = newStorageManager(bucketService, "/", logger)
			if storageErr != nil {
				fatal("failed to initialize storage manager", "err", storageErr)
			}
			invalidator.manager = storageManager
		}

		// Load configured bucket credentials if present.
		if len(cfg.Buckets) > 0 {
			encryptionEnabled, encErr := credentialcipher.CredentialEncryptionEnabled()
			if encErr != nil {
				fatal("invalid credential encryption configuration", "env", credentialcipher.CredentialMasterKeyEnv, "err", encErr)
			}
			if !encryptionEnabled {
				fatal("s3 credential encryption key is required", "env", credentialcipher.CredentialMasterKeyEnv)
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
				if err := bucketService.SaveS3Credential(cmd.Context(), cred); err != nil {
					logger.Error("failed to save s3 credential", "bucket", c.Bucket, "err", err)
				}
			}
		}
		if err := loadConfiguredBucketScopes(cmd.Context(), bucketService, bucketService, cfg.BucketScopes, logger); err != nil {
			fatal("failed to load configured bucket scopes", "err", err)
		}

		objectService := objects.NewService(backend.objectDependencies)
		usageService := usage.NewService(usage.Dependencies{
			Reports: backend.usageReports,
			Objects: objectService,
		})
		transferService := transfers.NewService(transfers.Dependencies{
			Access:      storageManager,
			Multipart:   storageManager,
			Scopes:      bucketService,
			Credentials: bucketService,
			Pending:     backend.pending,
			Events:      backend.usageIngest,
		})
		projectStorageService := projectstorage.NewService(
			projectstorage.Dependencies{
				Scopes:         bucketService,
				Credentials:    bucketService,
				Visibility:     bucketService,
				Inventory:      storageManager,
				Probe:          storageManager,
				Delete:         storageManager,
				Physical:       objectService,
				CleanupObjects: objectService,
				CleanupScopes:  bucketService,
			},
		)
		scopeRepairService := newScopeRepairService(objectService, bucketService, storageManager)

		// Build Fiber runtime and middleware pipeline.
		app := fiber.New(fiber.Config{
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   120 * time.Second,
			IdleTimeout:    120 * time.Second,
			ReadBufferSize: 64 * 1024,
			AppName:        "Syfon DRS Server",
		})
		app.Use(recover.New())

		// Init AuthZ Middleware
		// We use a standard slog.Logger for data-client compatibility
		slogLogger := logger
		authRuntime := authentication.NewRuntime(
			slogLogger,
			cfg.Auth.Mode,
			cfg.Auth.Basic.Username,
			cfg.Auth.Basic.Password,
		)
		authzMiddleware := middleware.NewAuthzMiddleware(slogLogger, middleware.Options{
			Mode:      cfg.Auth.Mode,
			Evaluator: authRuntime,
		})
		requestIDMiddleware := middleware.NewRequestIDMiddleware(slogLogger)

		rt := &serverRuntime{
			app:                 app,
			cfg:                 cfg,
			serviceInfo:         serviceInfoForBackend(cfg.Database.Sqlite != nil),
			objectService:       objectService,
			transferService:     transferService,
			usageService:        usageService,
			usageIngest:         backend.usageIngest,
			projectInspector:    projectStorageService.Inspector,
			projectCleanup:      projectStorageService.ProjectCleanup,
			scopeRepairService:  scopeRepairService,
			bucketService:       bucketService,
			authzMiddleware:     authzMiddleware,
			requestIDMiddleware: requestIDMiddleware,
		}
		registerServerRoutes(rt)

		addr := fmt.Sprintf(":%d", cfg.Port)
		logger.Info("server starting", "addr", addr)

		errCh := make(chan error, 1)
		go func() {
			if err := app.Listen(addr); err != nil {
				errCh <- err
			}
		}()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case err := <-errCh:
			fatal("server listen failed", "err", err)
		case sig := <-sigCh:
			logger.Info("shutdown signal received", "signal", sig.String())
		case <-cmd.Context().Done():
			logger.Info("shutdown requested by context cancellation")
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			fatal("server shutdown failed", "err", err)
		}
		logger.Info("server shutdown complete")
	},
}

type bucketScopeCreator interface {
	CreateBucketScope(context.Context, *buckets.Scope) error
}

func loadConfiguredBucketScopes(ctx context.Context, credentials buckets.CredentialReader, scopeStore bucketScopeCreator, scopes []config.BucketScopeConfig, logger *slog.Logger) error {
	if len(scopes) == 0 {
		return nil
	}
	logger.Info("loading configured bucket scopes", "count", len(scopes))
	for i, scope := range scopes {
		credentialID := strings.TrimSpace(scope.CredentialID)
		if credentialID == "" {
			credentialID = strings.TrimSpace(scope.Bucket)
		}
		cred, err := credentials.GetS3Credential(ctx, credentialID)
		if err != nil {
			return fmt.Errorf("bucket_scopes[%d] bucket=%s credential lookup failed: %w", i, scope.Bucket, err)
		}
		if cred == nil {
			return fmt.Errorf("bucket_scopes[%d] bucket=%s credential not found", i, scope.Bucket)
		}
		resolvedCredentialID := strings.TrimSpace(cred.CredentialID)
		if resolvedCredentialID == "" {
			resolvedCredentialID = strings.TrimSpace(cred.Bucket)
		}
		if err := scopeStore.CreateBucketScope(ctx, &buckets.Scope{
			Organization: scope.Organization,
			ProjectID:    scope.ProjectID,
			CredentialID: resolvedCredentialID,
			Bucket:       cred.Bucket,
			PathPrefix:   scope.PathPrefix,
		}); err != nil {
			return fmt.Errorf("bucket_scopes[%d] org=%s project=%s bucket=%s: %w", i, scope.Organization, scope.ProjectID, scope.Bucket, err)
		}
	}
	return nil
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

func isMockAuthEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("DRS_AUTH_MOCK_ENABLED"))
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
