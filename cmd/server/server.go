package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/store"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/calypr/syfon/internal/version"
)

func serviceInfoForConfig(cfg *config.Config) generated.N200ServiceInfo {
	if cfg == nil {
		return generated.N200ServiceInfo{}
	}
	service := cfg.Service
	environment := service.Environment
	if environment == "" {
		environment = cfg.Profile
	}
	if environment == "" {
		environment = "dev"
	}
	description := service.Description
	if description == "" {
		description = "Calypr-backed DRS server"
	}
	maxBulk := cfg.DRS.MaxBulkRequestLength
	enabled := cfg.Routes.Ga4gh
	disabled := false
	claimFormat := generated.DrsServiceDrsControlledAccessClaimFormat("ga4gh-passport-url-claim")
	claimDefault := generated.DrsServiceDrsControlledAccessDefault("open-access-read")
	now := time.Now().UTC()
	result := generated.N200ServiceInfo{
		Id:                   service.ID,
		Name:                 service.Name,
		Description:          &description,
		Version:              version.Version,
		Environment:          &environment,
		Type:                 generated.ServiceType{Group: "org.ga4gh", Artifact: "drs", Version: "1.5.0"},
		CreatedAt:            &now,
		UpdatedAt:            &now,
		MaxBulkRequestLength: maxBulk,
		Drs: &generated.DrsCapabilities{
			MaxBulkRequestLength:            maxBulk,
			ObjectRegistrationSupported:     &enabled,
			DeleteSupported:                 &enabled,
			DeleteStorageDataSupported:      &disabled,
			MetadataRetentionSupported:      &disabled,
			AccessMethodUpdateSupported:     &enabled,
			ChecksumAdditionSupported:       &disabled,
			FetchByChecksumSupported:        &enabled,
			ControlledAccessSupported:       &enabled,
			ControlledAccessClaimFormat:     &claimFormat,
			ControlledAccessDefault:         &claimDefault,
			ValidateAccessMethods:           &disabled,
			ValidateChecksums:               &disabled,
			ValidateFileSizes:               &disabled,
			MaxBulkAccessMethodUpdateLength: &maxBulk,
			MaxBulkDeleteLength:             &maxBulk,
			MaxRegisterRequestLength:        &maxBulk,
		},
	}
	result.Organization.Name = service.Organization
	result.Organization.Url = service.OrganizationURL
	if service.ContactURL != "" {
		result.ContactUrl = &service.ContactURL
	}
	if service.DocumentationURL != "" {
		result.DocumentationUrl = &service.DocumentationURL
	}
	return result
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
