package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/maintenance/scoperepair"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

func newScopeRepairService(objectService *objects.Service, bucketService *buckets.Service, storageManager storageProbe) *scoperepair.Service {
	adapter := scopeRepairIndexAdapter{service: objectService}
	return scoperepair.NewService(
		adapter,
		adapter,
		scopeRepairBucketsAdapter{service: bucketService},
		storageRepairInspector{probe: storageManager, buckets: bucketService},
		adapter,
	)
}

type scopeRepairIndexAdapter struct {
	service scopeRepairObjectService
}

type scopeRepairObjectService interface {
	ListPreparedObjectsPageByScope(context.Context, string, string, string, string, int, int) ([]objects.Record, error)
	GetObject(context.Context, string, string) (*objects.Record, error)
	ReplaceObjects(context.Context, []objects.Record) error
	CollapseProjectChecksumDuplicates(context.Context, string, string) (int, error)
}

var _ scoperepair.PreparedRecordReader = scopeRepairIndexAdapter{}
var _ scoperepair.ReferenceWriter = scopeRepairIndexAdapter{}
var _ scoperepair.DuplicateCollapser = scopeRepairIndexAdapter{}

func (a scopeRepairIndexAdapter) ListPrepared(ctx context.Context, query scoperepair.PreparedRecordQuery) ([]objects.Record, error) {
	return a.service.ListPreparedObjectsPageByScope(
		ctx,
		strings.TrimSpace(query.Organization),
		strings.TrimSpace(query.Project),
		"read",
		strings.TrimSpace(query.Start),
		query.Limit,
		0,
	)
}

func (a scopeRepairIndexAdapter) Update(ctx context.Context, id objects.RecordID, update objects.Record) error {
	existing, err := a.service.GetObject(ctx, string(id), "update")
	if err != nil {
		return err
	}
	merged, err := objects.MergeRecordUpdate(*existing, update, string(id), time.Now().UTC())
	if err != nil {
		return err
	}
	return a.service.ReplaceObjects(ctx, []objects.Record{merged})
}

func (a scopeRepairIndexAdapter) Collapse(ctx context.Context, organization, project string) (int, error) {
	return a.service.CollapseProjectChecksumDuplicates(ctx, organization, project)
}

type scopeRepairBucketsAdapter struct {
	service scopeRepairBucketService
}

type scopeRepairBucketService interface {
	ListS3Credentials(context.Context) ([]buckets.Credential, error)
	ListBucketScopes(context.Context) ([]buckets.Scope, error)
}

var _ scoperepair.ScopeReader = scopeRepairBucketsAdapter{}

func (a scopeRepairBucketsAdapter) ListCredentials(ctx context.Context) ([]buckets.Credential, error) {
	return a.service.ListS3Credentials(ctx)
}

func (a scopeRepairBucketsAdapter) ListScopes(ctx context.Context, bucket string) ([]buckets.Scope, error) {
	scopes, err := a.service.ListBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	requested := strings.TrimSpace(bucket)
	if requested == "" {
		return scopes, nil
	}
	filtered := make([]buckets.Scope, 0, len(scopes))
	for _, scope := range scopes {
		if strings.EqualFold(strings.TrimSpace(scope.Bucket), requested) || strings.EqualFold(strings.TrimSpace(scope.CredentialID), requested) {
			filtered = append(filtered, scope)
		}
	}
	return filtered, nil
}

type storageRepairInspector struct {
	probe   storageProbe
	buckets storageRepairBucketAccess
}

type storageProbe interface {
	Probe(context.Context, []storage.ProbeTarget) []storage.ProbeResult
}

type storageRepairBucketAccess interface {
	GetS3Credential(context.Context, string) (*buckets.Credential, error)
	ListVisibleBuckets(context.Context) (map[string]buckets.VisibleBucket, error)
}

var _ scoperepair.StorageProbe = storageRepairInspector{}

func (r storageRepairInspector) Inspect(ctx context.Context, req scoperepair.StorageInspectRequest) (scoperepair.StorageInspectResult, error) {
	rawURL := strings.TrimSpace(req.ObjectURL)
	bucket, key, ok := address.ParseS3URL(rawURL)
	if !ok {
		return scoperepair.StorageInspectResult{}, fmt.Errorf("object_url must be a valid s3://bucket/key URL")
	}
	if r.buckets != nil {
		credential, err := r.buckets.GetS3Credential(ctx, bucket)
		if err != nil {
			return scoperepair.StorageInspectResult{}, err
		}
		if credential == nil {
			return scoperepair.StorageInspectResult{}, fmt.Errorf("no stored bucket credential found for bucket %q", bucket)
		}
		if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
			return scoperepair.StorageInspectResult{}, fmt.Errorf("provider %q is not supported for server-backed add-url inspection", credential.Provider)
		}
		visible, err := r.buckets.ListVisibleBuckets(ctx)
		if err != nil {
			return scoperepair.StorageInspectResult{}, err
		}
		if !buckets.VisibleToCaller(visible, bucket, credential.CredentialID) {
			return scoperepair.StorageInspectResult{}, fmt.Errorf("bucket %q is not visible to the caller", bucket)
		}
	}
	if r.probe == nil {
		return scoperepair.StorageInspectResult{}, fmt.Errorf("storage probe is not configured")
	}
	results := r.probe.Probe(ctx, []storage.ProbeTarget{{ID: "object", Target: storage.ObjectTarget{Bucket: bucket, Key: key}}})
	if len(results) == 0 {
		return scoperepair.StorageInspectResult{}, fmt.Errorf("storage probe returned no result")
	}
	if err := results[0].Err; err != nil {
		if isStorageObjectNotFound(err) {
			return scoperepair.StorageInspectResult{}, scoperepair.ErrStorageObjectNotFound
		}
		return scoperepair.StorageInspectResult{}, err
	}
	return scoperepair.StorageInspectResult{ObjectURL: address.BucketToURL(bucket, key)}, nil
}

func isStorageObjectNotFound(err error) bool {
	var operation *storage.OperationError
	return errors.As(err, &operation) && operation.Kind == storage.ErrorNotFound && strings.TrimSpace(operation.Provider) != ""
}
