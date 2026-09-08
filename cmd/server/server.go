package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/store"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
)

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
	objectStore        objectrecords.ObjectStore
	bucketDependencies buckets.Dependencies
	pending            transferlfs.PendingStore
	usageIngest        usage.Ingestor
	usageReports       usage.ReportStore
}

type projectStorageCatalog struct {
	projectstorage.ScopeReader
	projectstorage.CredentialReader
	projectstorage.VisibilityReader
	projectstorage.PhysicalScopeReader
	projectstorage.ObjectScopeDeleter
	projectstorage.ScopeCatalog
}

var (
	errBucketVisibilityScopeQuery   = fmt.Errorf("bucket visibility fallback requires an object scope query")
	errBucketVisibilityRecordReader = fmt.Errorf("bucket visibility fallback requires an object record reader")
)

func newBucketVisibilityFallback(store objectrecords.ObjectStore) buckets.VisibilityFallback {
	return func(ctx context.Context) ([]buckets.VisibilityRow, error) {
		if store == nil {
			return nil, errBucketVisibilityScopeQuery
		}

		ids, err := store.ListObjectIDsByScope(ctx, "", "")
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []buckets.VisibilityRow{}, nil
		}

		records, err := store.GetBulkObjects(ctx, ids)
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

func sqliteServerBackend(database *store.Store) serverBackend {
	return serverBackend{
		objectStore: database,
		bucketDependencies: buckets.Dependencies{
			Credentials: database, CredentialAdmin: database, Scopes: database, Visibility: database,
		},
		pending:      database,
		usageIngest:  database,
		usageReports: database,
	}
}

func postgresServerBackend(database *store.Store) serverBackend {
	return serverBackend{
		objectStore: database,
		bucketDependencies: buckets.Dependencies{
			Credentials: database, CredentialAdmin: database, Scopes: database, Visibility: database,
		},
		pending:      database,
		usageIngest:  database,
		usageReports: database,
	}
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
