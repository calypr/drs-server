package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/store"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/calypr/syfon/internal/version"
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
		Version:     version.Version,
	}
}

type serverBackend struct {
	objectStore        objects.ObjectStore
	bucketDependencies buckets.Dependencies
	pending            transferlfs.PendingStore
	usageIngest        usage.Ingestor
	usageReports       usage.ReportStore
}

func serverBackendForStore(database *store.Store) serverBackend {
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
