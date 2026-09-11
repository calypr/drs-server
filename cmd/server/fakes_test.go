package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
)

type serverObjectStore struct {
	records map[string]*drs.DrsObject
	aliases map[string]string
}

func newServerObjectStore(records map[string]*drs.DrsObject) *serverObjectStore {
	store := &serverObjectStore{records: make(map[string]*drs.DrsObject), aliases: make(map[string]string)}
	for id, record := range records {
		store.records[id] = cloneServerRecord(record)
	}
	return store
}

func (s *serverObjectStore) GetObject(_ context.Context, id string) (*drs.DrsObject, error) {
	if _, found := s.records[id]; !found {
		id = s.aliases[id]
	}
	record, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return cloneServerRecord(record), nil
}

func (s *serverObjectStore) GetBulkObjects(_ context.Context, ids []string) ([]drs.DrsObject, error) {
	result := make([]drs.DrsObject, 0, len(ids))
	for _, id := range ids {
		if record, ok := s.records[id]; ok {
			result = append(result, *cloneServerRecord(record))
		}
	}
	return result, nil
}

func (s *serverObjectStore) GetPublicReadByIDs(_ context.Context, _ []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (s *serverObjectStore) DeleteObject(_ context.Context, id string) error {
	delete(s.records, id)
	return nil
}

func (s *serverObjectStore) CreateObject(_ context.Context, record *drs.DrsObject) error {
	if record == nil {
		return fmt.Errorf("record is required")
	}
	if s.records == nil {
		s.records = make(map[string]*drs.DrsObject)
	}
	s.records[record.Id] = cloneServerRecord(record)
	return nil
}

func (s *serverObjectStore) BulkDeleteObjects(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.DeleteObject(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *serverObjectStore) RegisterObjects(ctx context.Context, records []drs.DrsObject) error {
	for i := range records {
		if err := s.CreateObject(ctx, &records[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *serverObjectStore) ReplaceObjects(ctx context.Context, records []drs.DrsObject) error {
	s.records = make(map[string]*drs.DrsObject, len(records))
	return s.RegisterObjects(ctx, records)
}

func (s *serverObjectStore) UpdateObjectAccessMethods(_ context.Context, id string, methods []drs.AccessMethod) error {
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyMethods := append([]drs.AccessMethod(nil), methods...)
	record.AccessMethods = &copyMethods
	return nil
}

func (s *serverObjectStore) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]drs.AccessMethod) error {
	for id, methods := range updates {
		if err := s.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

func (s *serverObjectStore) RemoveObjectControlledAccess(_ context.Context, id, resource string) error {
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	if record.ControlledAccess == nil {
		return fmt.Errorf("%w: resource not found", errorapi.ErrNotFound)
	}
	filtered := make([]string, 0, len(*record.ControlledAccess))
	found := false
	for _, existing := range *record.ControlledAccess {
		if strings.TrimSpace(existing) == strings.TrimSpace(resource) {
			found = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !found {
		return fmt.Errorf("%w: resource not found", errorapi.ErrNotFound)
	}
	if len(filtered) == 0 {
		record.ControlledAccess = nil
	} else {
		record.ControlledAccess = &filtered
	}
	return nil
}

func (s *serverObjectStore) RemoveObjectControlledAccessBulk(ctx context.Context, ids []string, resource string) (int, error) {
	count := 0
	for _, id := range ids {
		if err := s.RemoveObjectControlledAccess(ctx, id, resource); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *serverObjectStore) CreateObjectAlias(_ context.Context, aliasID, canonicalID string) error {
	if _, ok := s.records[canonicalID]; !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	if s.aliases == nil {
		s.aliases = make(map[string]string)
	}
	s.aliases[aliasID] = canonicalID
	return nil
}

func (s *serverObjectStore) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := s.aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: alias not found", errorapi.ErrNotFound)
	}
	return canonicalID, nil
}

func (s *serverObjectStore) GetObjectsByChecksum(_ context.Context, checksum string) ([]drs.DrsObject, error) {
	result := make([]drs.DrsObject, 0)
	checksum = strings.TrimSpace(checksum)
	for id, record := range s.records {
		if id == checksum || record.Id == checksum || serverRecordHasChecksum(record, checksum) {
			result = append(result, *cloneServerRecord(record))
		}
	}
	return result, nil
}

func (s *serverObjectStore) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]drs.DrsObject, error) {
	result := make(map[string][]drs.DrsObject, len(checksums))
	for _, checksum := range checksums {
		matches, err := s.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func (s *serverObjectStore) ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	result := make(map[string][]string, len(checksums))
	for _, checksum := range checksums {
		matches, err := s.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		for _, record := range matches {
			if serverRecordInScope(&record, organization, project) {
				result[checksum] = append(result[checksum], record.Id)
			}
		}
	}
	return result, nil
}

func (s *serverObjectStore) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	result := make([]string, 0)
	for id, record := range s.records {
		if serverRecordInScope(record, organization, project) {
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *serverObjectStore) ListObjectIDsByResources(_ context.Context, resources []string, includeUnscoped bool) ([]string, error) {
	allowed := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		allowed[strings.TrimSpace(resource)] = struct{}{}
	}
	result := make([]string, 0)
	for id, record := range s.records {
		objectResources := objects.AccessResources(record)
		if len(objectResources) == 0 {
			if includeUnscoped {
				result = append(result, id)
			}
			continue
		}
		for _, resource := range objectResources {
			if _, ok := allowed[strings.TrimSpace(resource)]; ok {
				result = append(result, id)
				break
			}
		}
	}
	return result, nil
}

func (s *serverObjectStore) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	ids, err := s.ListObjectIDsByScope(ctx, organization, project)
	return pageServerIDs(ids, startAfter, limit, offset), err
}

func (s *serverObjectStore) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error) {
	ids := make([]string, 0)
	for id, record := range s.records {
		if organization != "" && !serverRecordInScope(record, organization, project) {
			continue
		}
		if !serverRecordHasURL(record, objectURL) {
			continue
		}
		ids = append(ids, id)
	}
	return pageServerIDs(ids, startAfter, limit, offset), nil
}

func serverRecordHasURL(record *drs.DrsObject, wanted string) bool {
	if record == nil || record.AccessMethods == nil {
		return false
	}
	for _, method := range *record.AccessMethods {
		if method.AccessUrl != nil && strings.TrimSpace(method.AccessUrl.Url) == strings.TrimSpace(wanted) {
			return true
		}
	}
	return false
}

func pageServerIDs(ids []string, startAfter string, limit, offset int) []string {
	start := 0
	for start < len(ids) && ids[start] <= startAfter {
		start++
	}
	start += offset
	if start > len(ids) {
		start = len(ids)
	}
	end := len(ids)
	if limit >= 0 && start+limit < end {
		end = start + limit
	}
	return append([]string(nil), ids[start:end]...)
}

func serverRecordHasChecksum(record *drs.DrsObject, checksum string) bool {
	for _, candidate := range record.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), checksum) {
			return true
		}
	}
	return false
}

func serverRecordInScope(record *drs.DrsObject, organization, project string) bool {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return true
	}
	for resource, projects := range clientaccess.ControlledAccessToAuthzMap(objects.AccessResources(record)) {
		if strings.TrimSpace(resource) != organization {
			continue
		}
		if project == "" || len(projects) == 0 {
			return true
		}
		for _, candidate := range projects {
			if candidate == project {
				return true
			}
		}
	}
	for _, resource := range objects.AccessResources(record) {
		if resource == "/programs/"+organization || resource == "/programs/"+organization+"/projects/"+project {
			return true
		}
	}
	return false
}

func cloneServerRecord(record *drs.DrsObject) *drs.DrsObject {
	if record == nil {
		return nil
	}
	copyRecord := *record
	copyRecord.Checksums = append([]drs.Checksum(nil), record.Checksums...)
	if record.AccessMethods != nil {
		methods := append([]drs.AccessMethod(nil), (*record.AccessMethods)...)
		copyRecord.AccessMethods = &methods
	}
	if record.ControlledAccess != nil {
		controlled := append([]string(nil), (*record.ControlledAccess)...)
		copyRecord.ControlledAccess = &controlled
	}
	return &copyRecord
}

var (
	_ objects.ObjectStore = (*serverObjectStore)(nil)
)

type serverTestDependencies struct {
	objects       objects.ObjectStore
	bucketService *buckets.Service
	usageIngest   usage.Ingestor
	usageReports  usage.ReportStore
	pending       transferlfs.PendingStore
}

func mockServerDependencies(objectStore *serverObjectStore, bucketStore *serverBucketStore) serverTestDependencies {
	bucketService, err := buckets.NewService(buckets.Dependencies{
		Credentials: bucketStore, CredentialAdmin: bucketStore, Scopes: bucketStore,
		Visibility: serverVisibilityQuery{},
	}, nil)
	if err != nil {
		panic(err)
	}
	return serverTestDependencies{
		objects:       objectStore,
		bucketService: bucketService,
		usageIngest:   serverUsageStore{},
		usageReports:  serverUsageStore{},
		pending:       serverPendingStore{},
	}
}

type serverVisibilityQuery struct{}

func (serverVisibilityQuery) ListBucketVisibilityRows(context.Context, []string, bool, bool) ([]buckets.VisibilityRow, error) {
	return nil, nil
}

type serverBucketStore struct {
	credentials map[string]buckets.Credential
	scopes      map[string]buckets.Scope
}

func (s *serverBucketStore) GetS3Credential(_ context.Context, id string) (*buckets.Credential, error) {
	if credential, ok := s.credentials[id]; ok {
		copyCredential := credential
		return &copyCredential, nil
	}
	for _, credential := range s.credentials {
		if strings.EqualFold(strings.TrimSpace(credential.Bucket), strings.TrimSpace(id)) {
			copyCredential := credential
			return &copyCredential, nil
		}
	}
	return nil, fmt.Errorf("%w: credential not found", errorapi.ErrNotFound)
}

func (s *serverBucketStore) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	result := make([]buckets.Credential, 0, len(s.credentials))
	for _, credential := range s.credentials {
		result = append(result, credential)
	}
	return result, nil
}

func (s *serverBucketStore) SaveS3Credential(_ context.Context, credential *buckets.Credential) error {
	if s.credentials == nil {
		s.credentials = make(map[string]buckets.Credential)
	}
	key := strings.TrimSpace(credential.CredentialID)
	if key == "" {
		key = strings.TrimSpace(credential.Bucket)
	}
	s.credentials[key] = *credential
	return nil
}

func (s *serverBucketStore) SaveBucketConfiguration(ctx context.Context, configuration buckets.BucketConfiguration) error {
	if err := s.SaveS3Credential(ctx, &configuration.Credential); err != nil {
		return err
	}
	return s.CreateBucketScope(ctx, &buckets.Scope{
		Organization: configuration.Organization,
		ProjectID:    configuration.ProjectID,
		CredentialID: configuration.Credential.CredentialID,
		Bucket:       configuration.Credential.Bucket,
		PathPrefix:   configuration.PathPrefix,
	})
}

func (s *serverBucketStore) DeleteS3Credential(_ context.Context, id string) error {
	delete(s.credentials, id)
	return nil
}

func (s *serverBucketStore) CreateBucketScope(_ context.Context, scope *buckets.Scope) error {
	if scope == nil {
		return fmt.Errorf("scope is required")
	}
	if s.scopes == nil {
		s.scopes = make(map[string]buckets.Scope)
	}
	s.scopes[scope.Organization+"|"+scope.ProjectID] = *scope
	return nil
}

func (s *serverBucketStore) DeleteBucketScope(_ context.Context, organization, project, credentialID, pathPrefix string) error {
	key := organization + "|" + project
	scope, ok := s.scopes[key]
	if !ok || (scope.CredentialID != credentialID && scope.Bucket != credentialID) || strings.Trim(scope.PathPrefix, "/") != strings.Trim(pathPrefix, "/") {
		return fmt.Errorf("%w: bucket scope not found", errorapi.ErrNotFound)
	}
	delete(s.scopes, key)
	return nil
}

func (s *serverBucketStore) GetBucketScope(_ context.Context, organization, project string) (*buckets.Scope, error) {
	scope, ok := s.scopes[organization+"|"+project]
	if !ok {
		return nil, fmt.Errorf("%w: bucket scope not found", errorapi.ErrNotFound)
	}
	copyScope := scope
	return &copyScope, nil
}

func (s *serverBucketStore) ListBucketScopes(context.Context) ([]buckets.Scope, error) {
	result := make([]buckets.Scope, 0, len(s.scopes))
	for _, scope := range s.scopes {
		result = append(result, scope)
	}
	return result, nil
}

var (
	_ buckets.CredentialReader = (*serverBucketStore)(nil)
	_ buckets.CredentialAdmin  = (*serverBucketStore)(nil)
	_ buckets.ScopeStore       = (*serverBucketStore)(nil)
)

type serverUsageStore struct{}

func (serverUsageStore) RecordFileUpload(context.Context, string) error   { return nil }
func (serverUsageStore) RecordFileDownload(context.Context, string) error { return nil }
func (serverUsageStore) RecordTransferAttributionEvents(context.Context, []usage.Event) error {
	return nil
}
func (serverUsageStore) RecordProviderTransferEvents(context.Context, []metricsapi.ProviderTransferEvent) error {
	return nil
}
func (serverUsageStore) GetFileUsage(context.Context, string) (*metricsapi.FileUsage, error) {
	return nil, fmt.Errorf("%w: file usage not found", errorapi.ErrNotFound)
}
func (serverUsageStore) ListFileUsageByObjectIDs(context.Context, []string) ([]metricsapi.FileUsage, error) {
	return []metricsapi.FileUsage{}, nil
}
func (serverUsageStore) ListFileUsage(context.Context, int, int, *time.Time) ([]metricsapi.FileUsage, error) {
	return []metricsapi.FileUsage{}, nil
}
func (serverUsageStore) GetFileUsageSummary(context.Context, *time.Time) (metricsapi.FileUsageSummary, error) {
	return metricsapi.FileUsageSummary{}, nil
}
func (serverUsageStore) ListFileUsagePageByScope(context.Context, string, string, int, int, *time.Time) ([]metricsapi.FileUsage, error) {
	return []metricsapi.FileUsage{}, nil
}
func (serverUsageStore) ListFileUsagePageByResources(context.Context, []string, bool, int, int, *time.Time) ([]metricsapi.FileUsage, error) {
	return []metricsapi.FileUsage{}, nil
}
func (serverUsageStore) GetFileUsageSummaryByScope(context.Context, string, string, *time.Time) (metricsapi.FileUsageSummary, error) {
	return metricsapi.FileUsageSummary{}, nil
}
func (serverUsageStore) GetFileUsageSummaryByResources(context.Context, []string, bool, *time.Time) (metricsapi.FileUsageSummary, error) {
	return metricsapi.FileUsageSummary{}, nil
}
func (serverUsageStore) GetProjectRecordSummaryByScope(context.Context, string, string) (metricsapi.FileUsageSummary, error) {
	return metricsapi.FileUsageSummary{}, nil
}
func (serverUsageStore) QueryTransferSummary(context.Context, usage.Filter, []string) (metricsapi.TransferAttributionSummary, error) {
	return metricsapi.TransferAttributionSummary{}, nil
}
func (serverUsageStore) QueryTransferBreakdown(context.Context, usage.Filter, string, []string) ([]metricsapi.TransferAttributionBreakdown, error) {
	return []metricsapi.TransferAttributionBreakdown{}, nil
}

var (
	_ usage.Ingestor    = serverUsageStore{}
	_ usage.ReportStore = serverUsageStore{}
)

type serverPendingStore struct{}

func (serverPendingStore) SavePendingMetadata(context.Context, []transferlfs.PendingMetadata) error {
	return nil
}
func (serverPendingStore) GetPendingMetadata(context.Context, string) (*transferlfs.PendingMetadata, error) {
	return nil, fmt.Errorf("%w: pending metadata not found", errorapi.ErrNotFound)
}
func (serverPendingStore) ConsumePendingMetadata(context.Context, transferlfs.PendingMetadata) (bool, error) {
	return true, nil
}

var _ transferlfs.PendingStore = serverPendingStore{}
