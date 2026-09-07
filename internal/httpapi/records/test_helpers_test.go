package records

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/gofiber/fiber/v3"
)

func ptr[T any](v T) *T { return &v }

type internalRecordStore struct {
	Objects     map[string]*objects.Record
	ObjectAuthz map[string]map[string][]string
	Aliases     map[string]string
}

var (
	_ objects.RecordReader          = (*internalRecordStore)(nil)
	_ objects.RecordWriter          = (*internalRecordStore)(nil)
	_ objects.AccessMethodWriter    = (*internalRecordStore)(nil)
	_ objects.AccessPolicyWriter    = (*internalRecordStore)(nil)
	_ objects.AliasStore            = (*internalRecordStore)(nil)
	_ objects.ContentReader         = (*internalRecordStore)(nil)
	_ objects.ChecksumScopeQuery    = (*internalRecordStore)(nil)
	_ objects.ScopeQuery            = (*internalRecordStore)(nil)
	_ objects.OptionalResourceQuery = (*internalRecordStore)(nil)
)

func (m *internalRecordStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := m.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
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
		m.ObjectAuthz[id] = cloneAuthzMap(obj.Authorizations)
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
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
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
		return fmt.Errorf("%w: object not found", faults.ErrNotFound)
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
	return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
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
	for _, resource := range syfoncommon.NormalizeAccessResources(resources) {
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
		return faults.ErrNotFound
	}
	targets := syfoncommon.NormalizeAccessResources([]string{resource})
	if len(targets) == 0 {
		return faults.ErrNotFound
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
		return faults.ErrNotFound
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
		m.ObjectAuthz[objectID] = syfoncommon.ControlledAccessToAuthzMap(filtered)
	}
	m.Objects[objectID] = &copyObj
	return nil
}

func (m *internalRecordStore) RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error) {
	targets := syfoncommon.NormalizeAccessResources([]string{resource})
	if len(targets) == 0 {
		return 0, faults.ErrNotFound
	}
	target := targets[0]
	orgWide := !strings.Contains(target, "/project/")
	count := 0
	for _, objectID := range objectIDs {
		obj, ok := m.Objects[objectID]
		if !ok {
			return count, faults.ErrNotFound
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

func (m *internalRecordStore) cloneObject(id string, obj *objects.Record) *objects.Record {
	copyObj := cloneRecord(*obj)
	if authz, ok := m.ObjectAuthz[id]; ok {
		copyObj.Authorizations = cloneAuthzMap(authz)
	}
	return &copyObj
}

func (m *internalRecordStore) objectResources(id string, obj *objects.Record) []string {
	if authz, ok := m.ObjectAuthz[id]; ok {
		return syfoncommon.AuthzMapToControlledAccess(authz)
	}
	if obj.ControlledAccess != nil {
		return syfoncommon.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return syfoncommon.AuthzMapToControlledAccess(obj.Authorizations)
}

func (m *internalRecordStore) objectMatchesScope(obj *objects.Record, organization, project string) bool {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return true
	}
	for _, resource := range m.objectResources(string(obj.Id), obj) {
		org, candidateProject, ok := syfoncommon.ResourceScope(resource)
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
	copyRecord.Authorizations = cloneAuthzMap(record.Authorizations)
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
	if record.Properties != nil {
		copyRecord.Properties = make(map[string]json.RawMessage, len(record.Properties))
		for key, value := range record.Properties {
			copyRecord.Properties[key] = append(json.RawMessage(nil), value...)
		}
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

func newInternalDRSInMemoryDB(t testing.TB) *sqlite.SqliteDB {
	t.Helper()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	return database
}

func withTestAuthzContext(req *http.Request, mode string, privileges map[string]map[string]bool) *http.Request {
	return req.WithContext(dataTestAuthContext(req.Context(), mode, mode == "gen3", privileges))
}

func dataTestAuthContext(base context.Context, mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
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

func policyTestContext(mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(context.Background(), session)
}

func doInternalDRSTestRequest(req *http.Request, fixture internalDRSTestFixture) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterRoutes(app, fixture.ObjectService)
	return runInternalDRSTestRequest(app, req)
}

func doInternalDRSTestRequestWithAlias(req *http.Request, fixture internalDRSTestFixture, method string, pattern string, handler fiber.Handler) *httptest.ResponseRecorder {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(req.Context())
		return c.Next()
	})
	RegisterRoutes(app, fixture.ObjectService)
	app.Add([]string{method}, pattern, handler)
	return runInternalDRSTestRequest(app, req)
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
