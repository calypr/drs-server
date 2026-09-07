package db

import (
	"context"
	"github.com/calypr/syfon/internal/models"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
)

// ServiceInfoStore exposes service metadata reads.
type ServiceInfoStore interface {
	GetServiceInfo(ctx context.Context) (*drs.Service, error)
}

// ObjectStore groups the object lifecycle and lookup capabilities used by the API layers.
type ObjectStore interface {
	GetObject(ctx context.Context, id string) (*models.InternalObject, error)
	DeleteObject(ctx context.Context, id string) error
	DeleteObjectAlias(ctx context.Context, aliasID string) error
	CreateObject(ctx context.Context, obj *models.InternalObject) error
	GetObjectsByChecksum(ctx context.Context, checksum string) ([]models.InternalObject, error)
	GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]models.InternalObject, error)
	ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error)
	ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error)
	CreateObjectAlias(ctx context.Context, aliasID, canonicalObjectID string) error
	ResolveObjectAlias(ctx context.Context, aliasID string) (string, error)
	GetBulkObjects(ctx context.Context, ids []string) ([]models.InternalObject, error)
	BulkDeleteObjects(ctx context.Context, ids []string) error
	RegisterObjects(ctx context.Context, objects []models.InternalObject) error
	ReplaceObjects(ctx context.Context, objects []models.InternalObject) error
	UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []drs.AccessMethod) error
	BulkUpdateAccessMethods(ctx context.Context, updates map[string][]drs.AccessMethod) error
	RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) error
	RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error)
}

type ObjectIDResourceLister interface {
	ListObjectIDsByResources(ctx context.Context, resources []string, includeUnscoped bool) ([]string, error)
}

type ObjectIDPageLister interface {
	ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error)
	ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error)
}

type ObjectChecksumPageLister interface {
	ListObjectIDsPageByChecksum(ctx context.Context, checksum, checksumType, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error)
}

type ObjectURLPageLister interface {
	ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error)
}

type ObjectAuthorizedLister interface {
	ListObjectIDsByScopeAndResources(ctx context.Context, organization, project string, resources []string, restrictToResources bool) ([]string, error)
	ListObjectIDsByChecksumsAndResources(ctx context.Context, checksums []string, resources []string, includeUnscoped, restrictToResources bool) (map[string][]string, error)
}

type FileUsageScopedLister interface {
	ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error)
	ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error)
	GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (models.FileUsageSummary, error)
	GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (models.FileUsageSummary, error)
	GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (models.FileUsageSummary, error)
}

type TransferAttributionScopedStore interface {
	GetTransferAttributionSummaryByResources(ctx context.Context, filter models.TransferAttributionFilter, resources []string) (models.TransferAttributionSummary, error)
	GetTransferAttributionBreakdownByResources(ctx context.Context, filter models.TransferAttributionFilter, groupBy string, resources []string) ([]models.TransferAttributionBreakdown, error)
}

type BucketVisibilityLister interface {
	ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]models.BucketVisibilityRow, error)
}

// CredentialStore groups bucket credential and scope management.
type CredentialStore interface {
	GetS3Credential(ctx context.Context, bucket string) (*models.S3Credential, error)
	ListS3Credentials(ctx context.Context) ([]models.S3Credential, error)
	SaveS3Credential(ctx context.Context, cred *models.S3Credential) error
	DeleteS3Credential(ctx context.Context, bucket string) error
	CreateBucketScope(ctx context.Context, scope *models.BucketScope) error
	DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error
	GetBucketScope(ctx context.Context, organization, projectID string) (*models.BucketScope, error)
	ListBucketScopes(ctx context.Context) ([]models.BucketScope, error)
}

// ObjectsAPIServiceDatabase is the storage surface used by the object service package.
type ObjectsAPIServiceDatabase interface {
	ServiceInfoStore
	ObjectStore
	CredentialStore
	UsageStore
}

// PendingLFSMetaStore manages pending LFS metadata.
type PendingLFSMetaStore interface {
	SavePendingLFSMeta(ctx context.Context, entries []models.PendingLFSMeta) error
	GetPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error)
	PopPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error)
}

// UsageStore manages file usage counters and summaries.
type UsageStore interface {
	RecordFileUpload(ctx context.Context, objectID string) error
	RecordFileDownload(ctx context.Context, objectID string) error
	RecordTransferAttributionEvents(ctx context.Context, events []models.TransferAttributionEvent) error
	RecordProviderTransferEvents(ctx context.Context, events []models.ProviderTransferEvent) error
	GetTransferAttributionSummary(ctx context.Context, filter models.TransferAttributionFilter) (models.TransferAttributionSummary, error)
	GetTransferAttributionBreakdown(ctx context.Context, filter models.TransferAttributionFilter, groupBy string) ([]models.TransferAttributionBreakdown, error)
	GetFileUsage(ctx context.Context, objectID string) (*models.FileUsage, error)
	ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]models.FileUsage, error)
	ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error)
	GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (models.FileUsageSummary, error)
}

// SHA256ValidityStore is the minimum storage surface needed by the SHA256 validity endpoint.
type SHA256ValidityStore interface {
	GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]models.InternalObject, error)
	ListS3Credentials(ctx context.Context) ([]models.S3Credential, error)
}

// MetricsStore is the minimum storage surface needed by the metrics API.
type MetricsStore interface {
	ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error)
	GetObject(ctx context.Context, id string) (*models.InternalObject, error)
	RecordTransferAttributionEvents(ctx context.Context, events []models.TransferAttributionEvent) error
	RecordProviderTransferEvents(ctx context.Context, events []models.ProviderTransferEvent) error
	ListS3Credentials(ctx context.Context) ([]models.S3Credential, error)
	GetTransferAttributionSummary(ctx context.Context, filter models.TransferAttributionFilter) (models.TransferAttributionSummary, error)
	GetTransferAttributionBreakdown(ctx context.Context, filter models.TransferAttributionFilter, groupBy string) ([]models.TransferAttributionBreakdown, error)
	GetFileUsage(ctx context.Context, objectID string) (*models.FileUsage, error)
	ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]models.FileUsage, error)
	ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error)
	GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (models.FileUsageSummary, error)
}

// LFSStore is the minimum storage surface needed by the LFS API.
type LFSStore interface {
	ObjectStore
	CredentialStore
	PendingLFSMetaStore
	UsageStore
}

// DatabaseInterface defines the full database backend contract.
type DatabaseInterface interface {
	ServiceInfoStore
	ObjectStore
	CredentialStore
	PendingLFSMetaStore
	UsageStore
}
