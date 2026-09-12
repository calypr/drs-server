package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	providerstorage "github.com/calypr/syfon/internal/storage"
	"github.com/gofiber/fiber/v3"
)

const maintenanceRouteSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type maintenanceRouteProvider struct{}

func (maintenanceRouteProvider) Probe(_ context.Context, targets []providerstorage.ProbeTarget) []providerstorage.ProbeResult {
	results := make([]providerstorage.ProbeResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, providerstorage.ProbeResult{
			ID:     target.ID,
			Target: target.Target,
			Metadata: providerstorage.ObjectMetadata{
				Provider:   "s3",
				Bucket:     target.Target.PhysicalBucket,
				Key:        target.Target.Key,
				SizeBytes:  0,
				MetaSHA256: maintenanceRouteSHA,
			},
		})
	}
	return results
}

func (maintenanceRouteProvider) Inventory(_ context.Context, request providerstorage.InventoryRequest) (providerstorage.InventoryResult, error) {
	return providerstorage.InventoryResult{
		Items: []providerstorage.ObjectMetadata{{
			Provider:   "s3",
			Bucket:     request.Target.PhysicalBucket,
			Key:        request.Prefix,
			SizeBytes:  0,
			MetaSHA256: maintenanceRouteSHA,
		}},
		Complete: true,
	}, nil
}

func newMaintenanceRouteApp(t *testing.T) *fiber.App {
	t.Helper()
	credentialStore := &bucketTestStore{Credentials: map[string]domainbuckets.Credential{
		"credential": {CredentialID: "credential", Bucket: "bucket", Provider: "s3"},
	}}
	bucketService := newInternalDRSObjectManager(credentialStore).bucketService
	storageService := projectstorage.NewService(projectstorage.Dependencies{
		Credentials: bucketService,
		Visibility:  bucketService,
		Providers:   projectstorage.Providers{Probe: maintenanceRouteProvider{}, Inventory: maintenanceRouteProvider{}},
	})
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	RegisterRoutes(app, Dependencies{ProjectStorage: storageService}, Options{Internal: true})
	return app
}

func TestProjectDeleteRouteHasOneGeneratedOwner(t *testing.T) {
	app := fiber.New()
	RegisterRoutes(app, Dependencies{}, Options{Internal: true})

	const path = "/data/projects/:organization/:project_id"
	count := 0
	for _, methodRoutes := range app.Stack() {
		for _, route := range methodRoutes {
			if route.Method == http.MethodDelete && route.Path == path {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("DELETE %s route count = %d, want 1", path, count)
	}
}

type maintenanceCleanupObjects struct {
	deleted []string
}

func (f *maintenanceCleanupObjects) DeleteBulkByScope(_ context.Context, organization, project string) (int, error) {
	f.deleted = append(f.deleted, organization+"/"+project)
	return 2, nil
}

type maintenanceCleanupScopes struct {
	deleted []string
}

func (f *maintenanceCleanupScopes) ListBucketScopes(context.Context) ([]domainbuckets.Scope, error) {
	return []domainbuckets.Scope{{Organization: "org", ProjectID: "project", CredentialID: "credential"}}, nil
}

func (f *maintenanceCleanupScopes) DeleteBucketScope(_ context.Context, organization, project, credential, path string) error {
	f.deleted = append(f.deleted, strings.Join([]string{organization, project, credential, path}, "/"))
	return nil
}

func TestBucketProjectDeleteRouteRunsProjectCleanup(t *testing.T) {
	objects := &maintenanceCleanupObjects{}
	scopes := &maintenanceCleanupScopes{}
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	RegisterRoutes(app, Dependencies{ProjectStorage: projectstorage.NewService(projectstorage.Dependencies{
		ObjectCleanup: objects,
		ScopeCatalog:  scopes,
	})}, Options{Internal: true})

	request := httptest.NewRequest(http.MethodDelete, "/data/projects/org/project", nil)
	request = request.WithContext(dataTestAuthContext(request.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org/projects/project": {"delete": true},
	}))
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("DELETE project data: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want 200: %s", response.StatusCode, payload)
	}
	var result bucketapi.DeleteProjectDataResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Organization != "org" || result.ProjectId != "project" || result.DeletedObjects != 2 || result.DeletedBucketScopes != 1 {
		t.Fatalf("response = %+v", result)
	}
	if len(objects.deleted) != 1 || objects.deleted[0] != "org/project" || len(scopes.deleted) != 1 || scopes.deleted[0] != "org/project/credential/" {
		t.Fatalf("cleanup calls = objects:%v scopes:%v", objects.deleted, scopes.deleted)
	}
}

func TestMaintenanceInspectBoundaryCharacterization(t *testing.T) {
	tests := []struct {
		name, path, body string
		status           int
		check            func(*testing.T, []byte)
	}{
		{
			name:   "single trims URL and ignores expected name",
			path:   "/data/inspect",
			body:   `{"id":"single","object_url":" s3://bucket/prefix/file.txt ","organization":"ignored","project":"ignored","key":"ignored","scheme":"ignored","expected_name":"wrong.txt","expected_sha256":" sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "}`,
			status: 200,
			check: func(t *testing.T, payload []byte) {
				var response internalapi.InternalInspectObjectResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if response.ObjectUrl != "s3://bucket/prefix/file.txt" || response.Key != "prefix/file.txt" {
					t.Fatalf("response = %+v", response)
				}
			},
		},
		{
			name:   "bulk ignores expected name and honors trimmed hash",
			path:   "/data/inspect/bulk",
			body:   `{"items":[{"id":"bulk","object_url":" s3://bucket/prefix/file.txt ","organization":"ignored","project":"ignored","key":"ignored","scheme":"ignored","expected_name":"wrong.txt","expected_sha256":" sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "}]}`,
			status: 200,
			check: func(t *testing.T, payload []byte) {
				var response internalapi.InternalInspectObjectBulkResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(response.Items) != 1 {
					t.Fatalf("items = %+v", response.Items)
				}
				item := response.Items[0]
				if item.ValidationStatus != "matched" || item.Sha256Match == nil || !*item.Sha256Match || item.NameMatch != nil || item.SizeMatch != nil {
					t.Fatalf("bulk item = %+v, want hash match without name or size validation", item)
				}
			},
		},
		{
			name:   "bulk explicit zero size differs from omitted size",
			path:   "/data/inspect/bulk",
			body:   `{"items":[{"id":"bulk-zero","object_url":"s3://bucket/prefix/file.txt","expected_name":"wrong.txt","expected_size_bytes":0}]}`,
			status: 200,
			check: func(t *testing.T, payload []byte) {
				var response internalapi.InternalInspectObjectBulkResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				item := response.Items[0]
				if item.ValidationStatus != "matched" || item.SizeMatch == nil || !*item.SizeMatch || item.NameMatch != nil {
					t.Fatalf("bulk item = %+v, want explicit zero-size match without name validation", item)
				}
			},
		},
		{
			name:   "bulk list ignores non-list fields",
			path:   "/data/inspect/bulk-list",
			body:   `{"items":[{"id":"list","object_url":"s3://bucket/prefix/file.txt","organization":"invalid","project":"invalid","key":"invalid","scheme":"file","expected_name":"file.txt","expected_size_bytes":0,"expected_sha256":"sha256:not-the-remote-hash"}]}`,
			status: 200,
			check: func(t *testing.T, payload []byte) {
				var response internalapi.InternalInspectObjectBulkResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				item := response.Items[0]
				if item.ValidationStatus != "matched" || item.SizeMatch == nil || !*item.SizeMatch || item.NameMatch == nil || !*item.NameMatch || item.Sha256Match != nil {
					t.Fatalf("bulk-list item = %+v, want size/name matches without SHA validation", item)
				}
			},
		},
		{
			name:   "strict unknown field",
			path:   "/data/inspect/bulk",
			body:   `{"items":[{"object_url":"s3://bucket/prefix/file.txt","unexpected":true}]}`,
			status: 400,
			check: func(t *testing.T, payload []byte) {
				if !strings.Contains(string(payload), "unexpected") {
					t.Fatalf("error response = %s", payload)
				}
			},
		},
		{
			name:   "empty bulk items",
			path:   "/data/inspect/bulk",
			body:   `{"items":[]}`,
			status: 400,
		},
		{
			name:   "empty bulk-list items",
			path:   "/data/inspect/bulk-list",
			body:   `{"items":[]}`,
			status: 400,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newMaintenanceRouteApp(t)
			request := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			payload, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, test.status, payload)
			}
			if test.check != nil {
				test.check(t, payload)
			}
		})
	}
}
