package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func ptr[T any](v T) *T { return &v }

type internalRecordStore struct {
	Objects     map[string]*objects.Record
	ObjectAuthz map[string]map[string][]string
	Aliases     map[string]string
}

var (
	_ objectrecords.ObjectStore = (*internalRecordStore)(nil)
)

func (m *internalRecordStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := m.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return m.cloneObject(id, obj), nil
}

func (m *internalRecordStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	out := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if obj, ok := m.Objects[id]; ok {
			out = append(out, *m.cloneObject(id, obj))
		}
	}
	return out, nil
}

func (m *internalRecordStore) DeleteObject(_ context.Context, id string) error {
	delete(m.Objects, id)
	delete(m.ObjectAuthz, id)
	return nil
}

func (m *internalRecordStore) CreateObject(_ context.Context, obj *objects.Record) error {
	if obj == nil {
		return fmt.Errorf("object is required")
	}
	return m.RegisterObjects(context.Background(), []objects.Record{*obj})
}

func (m *internalRecordStore) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(m.Objects, id)
		delete(m.ObjectAuthz, id)
	}
	return nil
}

func (m *internalRecordStore) RegisterObjects(_ context.Context, records []objects.Record) error {
	if m.Objects == nil {
		m.Objects = make(map[string]*objects.Record)
	}
	if m.ObjectAuthz == nil {
		m.ObjectAuthz = make(map[string]map[string][]string)
	}
	for _, obj := range records {
		id := string(obj.Id)
		copyObj := cloneRecord(obj)
		m.Objects[id] = &copyObj
		m.ObjectAuthz[id] = clientaccess.ControlledAccessToAuthzMap(objects.AccessResources(&obj))
	}
	return nil
}

func (m *internalRecordStore) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	if err := m.RegisterObjects(ctx, records); err != nil {
		return err
	}
	for _, obj := range records {
		if obj.AccessMethods != nil {
			if err := m.UpdateObjectAccessMethods(ctx, string(obj.Id), *obj.AccessMethods); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *internalRecordStore) UpdateObjectAccessMethods(_ context.Context, objectID string, methods []objects.AccessMethod) error {
	obj, ok := m.Objects[objectID]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyObj := cloneRecord(*obj)
	copyObj.AccessMethods = cloneAccessMethods(methods)
	m.Objects[objectID] = &copyObj
	return nil
}

func (m *internalRecordStore) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	for id, methods := range updates {
		if err := m.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

func (m *internalRecordStore) DeleteObjectAlias(_ context.Context, aliasID string) error {
	delete(m.Aliases, aliasID)
	return nil
}

func (m *internalRecordStore) CreateObjectAlias(_ context.Context, aliasID, canonicalID string) error {
	if _, ok := m.Objects[canonicalID]; !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	if m.Aliases == nil {
		m.Aliases = make(map[string]string)
	}
	m.Aliases[aliasID] = canonicalID
	return nil
}

func (m *internalRecordStore) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	if canonicalID, ok := m.Aliases[aliasID]; ok {
		return canonicalID, nil
	}
	return "", fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
}

func (m *internalRecordStore) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	out := make([]objects.Record, 0)
	for id, obj := range m.Objects {
		if id == checksum || string(obj.Id) == checksum || recordHasChecksum(obj, checksum) {
			out = append(out, *m.cloneObject(id, obj))
		}
	}
	return out, nil
}

func (m *internalRecordStore) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	out := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := m.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		out[checksum] = matches
	}
	return out, nil
}

func (m *internalRecordStore) ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	out := make(map[string][]string, len(checksums))
	for _, checksum := range checksums {
		matches, err := m.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		for _, obj := range matches {
			if m.objectMatchesScope(&obj, organization, project) {
				out[checksum] = append(out[checksum], string(obj.Id))
			}
		}
	}
	return out, nil
}

func (m *internalRecordStore) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	ids := make([]string, 0, len(m.Objects))
	for id, obj := range m.Objects {
		if m.objectMatchesScope(obj, organization, project) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *internalRecordStore) ListObjectIDsByResources(_ context.Context, resources []string, includeUnscoped bool) ([]string, error) {
	allowed := make(map[string]struct{}, len(resources))
	for _, resource := range clientaccess.NormalizeAccessResources(resources) {
		allowed[resource] = struct{}{}
	}
	ids := make([]string, 0, len(m.Objects))
	for id, obj := range m.Objects {
		objectResources := m.objectResources(id, obj)
		if len(objectResources) == 0 {
			if includeUnscoped {
				ids = append(ids, id)
			}
			continue
		}
		for _, resource := range objectResources {
			if _, ok := allowed[resource]; ok {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids, nil
}

func (m *internalRecordStore) RemoveObjectControlledAccess(_ context.Context, objectID, resource string) error {
	obj, ok := m.Objects[objectID]
	if !ok {
		return errorapi.ErrNotFound
	}
	targets := clientaccess.NormalizeAccessResources([]string{resource})
	if len(targets) == 0 {
		return errorapi.ErrNotFound
	}
	target := targets[0]
	resources := m.objectResources(objectID, obj)
	filtered := make([]string, 0, len(resources))
	found := false
	for _, existing := range resources {
		if existing == target {
			found = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !found {
		return errorapi.ErrNotFound
	}
	copyObj := cloneRecord(*obj)
	if len(filtered) == 0 {
		copyObj.ControlledAccess = nil
		delete(m.ObjectAuthz, objectID)
	} else {
		copyObj.ControlledAccess = ptr(append([]string(nil), filtered...))
		if m.ObjectAuthz == nil {
			m.ObjectAuthz = make(map[string]map[string][]string)
		}
		m.ObjectAuthz[objectID] = clientaccess.ControlledAccessToAuthzMap(filtered)
	}
	m.Objects[objectID] = &copyObj
	return nil
}

func (m *internalRecordStore) RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error) {
	targets := clientaccess.NormalizeAccessResources([]string{resource})
	if len(targets) == 0 {
		return 0, errorapi.ErrNotFound
	}
	target := targets[0]
	orgWide := !strings.Contains(target, "/project/")
	count := 0
	for _, objectID := range objectIDs {
		obj, ok := m.Objects[objectID]
		if !ok {
			return count, errorapi.ErrNotFound
		}
		for _, existing := range m.objectResources(objectID, obj) {
			if existing != target && (!orgWide || !strings.HasPrefix(existing, target+"/project/")) {
				continue
			}
			if err := m.RemoveObjectControlledAccess(ctx, objectID, existing); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

func (m *internalRecordStore) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	ids, err := m.ListObjectIDsByScope(ctx, organization, project)
	return pageRecordIDs(ids, startAfter, limit, offset), err
}

func (m *internalRecordStore) ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error) {
	ids, err := m.ListObjectIDsByResources(ctx, resources, includeUnscoped)
	return pageRecordIDs(ids, startAfter, limit, offset), err
}

func (m *internalRecordStore) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error) {
	ids := make([]string, 0)
	for id, obj := range m.Objects {
		if !recordHasAccessURL(obj, objectURL) || (organization != "" && !m.objectMatchesScope(obj, organization, project)) {
			continue
		}
		if restrictToResources {
			allowed, err := m.ListObjectIDsByResources(ctx, resources, includeUnscoped)
			if err != nil {
				return nil, err
			}
			found := false
			for _, candidate := range allowed {
				if candidate == id {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		ids = append(ids, id)
	}
	return pageRecordIDs(ids, startAfter, limit, offset), nil
}

func recordHasAccessURL(obj *objects.Record, wanted string) bool {
	if obj == nil || obj.AccessMethods == nil {
		return false
	}
	for _, method := range *obj.AccessMethods {
		if method.AccessUrl != nil && strings.TrimSpace(method.AccessUrl.Url) == strings.TrimSpace(wanted) {
			return true
		}
	}
	return false
}

func (m *internalRecordStore) ListObjectIDsByScopeAndResources(ctx context.Context, organization, project string, resources []string, includeUnscoped bool) ([]string, error) {
	ids, err := m.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	allowed, err := m.ListObjectIDsByResources(ctx, resources, includeUnscoped)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	filtered := ids[:0]
	for _, id := range ids {
		if _, ok := set[id]; ok {
			filtered = append(filtered, id)
		}
	}
	return filtered, nil
}

func (m *internalRecordStore) ListObjectIDsByChecksumsAndResources(ctx context.Context, checksums, resources []string, includeUnscoped, restrictToResources bool) (map[string][]string, error) {
	result := make(map[string][]string, len(checksums))
	allowed, err := m.ListObjectIDsByResources(ctx, resources, includeUnscoped)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	for _, checksum := range checksums {
		objectsForChecksum, err := m.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		for _, obj := range objectsForChecksum {
			id := string(obj.Id)
			if !restrictToResources || len(resources) == 0 {
				result[checksum] = append(result[checksum], id)
				continue
			}
			if _, ok := set[id]; ok {
				result[checksum] = append(result[checksum], id)
			}
		}
	}
	return result, nil
}

func pageRecordIDs(ids []string, startAfter string, limit, offset int) []string {
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

func (m *internalRecordStore) cloneObject(id string, obj *objects.Record) *objects.Record {
	copyObj := cloneRecord(*obj)
	if authz, ok := m.ObjectAuthz[id]; ok {
		controlled := clientaccess.AuthzMapToControlledAccess(authz)
		copyObj.ControlledAccess = &controlled
	}
	return &copyObj
}

func (m *internalRecordStore) objectResources(id string, obj *objects.Record) []string {
	if authz, ok := m.ObjectAuthz[id]; ok {
		return clientaccess.AuthzMapToControlledAccess(authz)
	}
	if obj.ControlledAccess != nil {
		return clientaccess.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return objects.AccessResources(obj)
}

func (m *internalRecordStore) objectMatchesScope(obj *objects.Record, organization, project string) bool {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return true
	}
	for _, resource := range m.objectResources(string(obj.Id), obj) {
		org, candidateProject, ok := clientaccess.ResourceScope(resource)
		if ok && org == organization && (project == "" || candidateProject == project) {
			return true
		}
	}
	return false
}

func recordHasChecksum(obj *objects.Record, checksum string) bool {
	for _, candidate := range obj.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
}

func cloneRecord(record objects.Record) objects.Record {
	copyRecord := record
	copyRecord.Checksums = append([]objects.Checksum(nil), record.Checksums...)
	copyRecord.NameAliases = append([]string(nil), record.NameAliases...)
	if record.AccessMethods != nil {
		copyRecord.AccessMethods = cloneAccessMethods(*record.AccessMethods)
	}
	if record.ControlledAccess != nil {
		copyRecord.ControlledAccess = ptr(append([]string(nil), (*record.ControlledAccess)...))
	}
	if record.Aliases != nil {
		copyRecord.Aliases = ptr(append([]string(nil), (*record.Aliases)...))
	}
	if record.Contents != nil {
		contents := append([]objects.Content(nil), (*record.Contents)...)
		copyRecord.Contents = &contents
	}
	return copyRecord
}

func cloneAccessMethods(methods []objects.AccessMethod) *[]objects.AccessMethod {
	copyMethods := append([]objects.AccessMethod(nil), methods...)
	return &copyMethods
}

func cloneAuthzMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for organization, projects := range in {
		out[organization] = append([]string(nil), projects...)
	}
	return out
}

func newInternalDRSInMemoryDB(t testing.TB) *store.Store {
	t.Helper()
	cipher, err := credentialcipher.NewFromEnv()
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	database, err := sqlite.NewSqliteDB(":memory:", cipher)
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	return database
}

func recordsWithTestAuthzContext(req *http.Request, mode string, privileges map[string]map[string]bool) *http.Request {
	return req.WithContext(recordsDataTestAuthContext(req.Context(), mode, mode == "gen3", privileges))
}

func recordsDataTestAuthContext(base context.Context, mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	sessionMode := mode
	if mode == "local-authz" {
		sessionMode = "local"
	}
	session := access.NewSession(sessionMode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = sessionMode == "gen3" || mode == "local-authz"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func recordsDoInternalDRSTestRequest(req *http.Request, fixture recordsInternalDRSTestFixture) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	registerRecordRoutes(app, fixture.ObjectService)
	return runInternalDRSTestRequest(app, req)
}

func registerRecordRoutes(router fiber.Router, objectService *objectrecords.Service) {
	internalapi.RegisterHandlers(router, &internalServer{objects: objectService})
}

func runInternalDRSTestRequest(app *fiber.App, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	resp, err := app.Test(req)
	if err != nil {
		rr.WriteHeader(http.StatusInternalServerError)
		_, _ = rr.WriteString(err.Error())
		return rr
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			rr.Header().Add(key, value)
		}
	}
	rr.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rr, resp.Body)
	return rr
}

var _ domaintransfers.StoragePort = (*internalDRSStorageFake)(nil)

type internalDRSStorageFake struct {
	mu sync.Mutex

	bucket        string
	key           string
	signURL       string
	signID        string
	signOpts      storage.SignRequest
	completeErr   error
	completeParts []storage.CompletedPart
}

func transfersWithTestAuthzContext(req *http.Request, mode string, privileges map[string]map[string]bool) *http.Request {
	return req.WithContext(transfersDataTestAuthContext(req.Context(), mode, mode == "gen3", privileges))
}

func transfersDataTestAuthContext(base context.Context, mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	sessionMode := mode
	if mode == "local-authz" {
		sessionMode = "local"
	}
	session := access.NewSession(sessionMode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = sessionMode == "gen3" || mode == "local-authz"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func (m *internalDRSStorageFake) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	m.mu.Lock()
	m.signID = request.Target.LookupKey
	m.signURL = request.Target.OriginalURL
	m.signOpts = request
	m.mu.Unlock()
	suffix := "?signed=true"
	if strings.EqualFold(strings.TrimSpace(request.Method), http.MethodPut) || strings.EqualFold(strings.TrimSpace(request.Method), http.MethodPost) {
		suffix += "&upload=true"
	}
	if request.Range != nil {
		suffix += fmt.Sprintf("&range=%d-%d", request.Range.Start, request.Range.End)
	}
	return storage.SignedAccess{Location: request.Target.OriginalURL + suffix}, nil
}

func (m *internalDRSStorageFake) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	m.mu.Lock()
	m.bucket = target.PhysicalBucket
	m.key = target.Key
	m.mu.Unlock()
	return storage.UploadID("mock-upload-id"), nil
}

func (m *internalDRSStorageFake) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	m.mu.Lock()
	m.bucket = request.Target.PhysicalBucket
	m.key = request.Target.Key
	m.mu.Unlock()
	return storage.SignedAccess{Location: fmt.Sprintf("s3://%s/%s?uploadId=%s&partNumber=%d", request.Target.PhysicalBucket, request.Target.Key, request.UploadID, request.PartNumber)}, nil
}

func (m *internalDRSStorageFake) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	m.mu.Lock()
	m.bucket = request.Target.PhysicalBucket
	m.key = request.Target.Key
	m.completeParts = append([]storage.CompletedPart(nil), request.Parts...)
	err := m.completeErr
	m.mu.Unlock()
	return err
}

func transfersDoInternalDRSTestRequest(req *http.Request, fixture transfersInternalDRSTestFixture) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	registerTransferRoutes(app, fixture.ObjectService, fixture.TransferService)

	rr := httptest.NewRecorder()
	resp, err := app.Test(req)
	if err != nil {
		rr.WriteHeader(http.StatusInternalServerError)
		_, _ = rr.WriteString(err.Error())
		return rr
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			rr.Header().Add(k, v)
		}
	}
	rr.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rr, resp.Body)
	return rr
}

func registerTransferRoutes(router fiber.Router, objectService *objectrecords.Service, transferService *domaintransfers.Service) {
	internalapi.RegisterHandlers(router, &internalServer{objects: objectService, transfers: transferService})
}
