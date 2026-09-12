package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func uploadSecurityApp(t *testing.T, service *transfers.Service, auth config.AuthConfig) *fiber.App {
	t.Helper()
	runtime := authentication.NewRuntime(nil, auth)
	t.Cleanup(runtime.Close)
	app := fiber.New()
	RegisterRoutes(app, Dependencies{Transfers: service, Authorization: AuthorizationHandler(AuthzOptions{Mode: auth.Mode, Evaluator: runtime})}, Options{Internal: true})
	return app
}

func uploadSecurityRequest(t *testing.T, app *fiber.App, method, path, body, authorization string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestMultipartRoutesRejectAnonymousRequests(t *testing.T) {
	provider := &lfsTestStorage{}
	service := transfers.NewService(transfers.Dependencies{Objects: objects.NewService(newDRSObjectStore(t, nil)), Storage: provider, Scopes: &multipartTestScope{prefix: "private-project"}})
	app := uploadSecurityApp(t, service, config.AuthConfig{Mode: "gen3"})

	status, body := uploadSecurityRequest(t, app, http.MethodPost, "/data/multipart/init", `{"key":"new/payload","organization":"victim","project":"private"}`, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous multipart init status = %d, want 401; body=%s", status, body)
	}

	for _, request := range []struct {
		path string
		body string
	}{
		{path: "/data/multipart/upload", body: `{"uploadId":"unknown","partNumber":1}`},
		{path: "/data/multipart/complete", body: `{"uploadId":"unknown","parts":[{"partNumber":1,"etag":"etag"}]}`},
	} {
		status, body = uploadSecurityRequest(t, app, http.MethodPost, request.path, request.body, "")
		if status != http.StatusUnauthorized {
			t.Fatalf("anonymous %s status = %d, want 401; body=%s", request.path, status, body)
		}
	}
}

func TestMultipartInitRequiresExistingObjectUpdateAccess(t *testing.T) {
	methods := []drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://private-bucket/object"}}}
	resources := []string{"/organization/victim/project/private"}
	record := &drs.DrsObject{Id: "existing-record", CreatedTime: time.Now().UTC(), Size: 10, AccessMethods: &methods, ControlledAccess: &resources}
	service := transfers.NewService(transfers.Dependencies{Objects: objects.NewService(newDRSObjectStore(t, map[string]*drs.DrsObject{record.Id: record})), Storage: &lfsTestStorage{}, Events: drsTestTransferEvents{}})
	app := uploadSecurityApp(t, service, config.AuthConfig{Mode: "gen3", Mock: config.MockAuthConfig{Enabled: true, RequireAuthHeader: true, Resources: resources, Methods: []string{"read"}}})

	status, body := uploadSecurityRequest(t, app, http.MethodPost, "/data/multipart/init", `{"guid":"existing-record"}`, "Bearer synthetic")
	if status != http.StatusForbidden {
		t.Fatalf("read-only multipart init status = %d, want 403; body=%s", status, body)
	}
}

func TestUploadSigningRequiresDestinationScopeAccess(t *testing.T) {
	service := transfers.NewService(transfers.Dependencies{Objects: objects.NewService(newDRSObjectStore(t, nil)), Storage: &lfsTestStorage{}, Scopes: &multipartTestScope{prefix: "victim-project"}})
	app := uploadSecurityApp(t, service, config.AuthConfig{Mode: "gen3", Mock: config.MockAuthConfig{Enabled: true, RequireAuthHeader: true, Resources: []string{"/organization/other/project/other"}, Methods: []string{"read"}}})

	status, body := uploadSecurityRequest(t, app, http.MethodGet, "/data/upload/new-object?organization=victim&project=private&key=object", "", "Bearer synthetic")
	if status != http.StatusForbidden {
		t.Fatalf("cross-project upload signing status = %d, want 403; body=%s", status, body)
	}

	status, body = uploadSecurityRequest(t, app, http.MethodPost, "/data/upload/bulk", `{"requests":[{"file_id":"new-object","organization":"victim","project":"private","key":"object"}]}`, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous bulk upload status = %d, want 401; body=%s", status, body)
	}
}

func TestMultipartSessionSurvivesServiceBoundaryAndCompletionRetry(t *testing.T) {
	database := newDRSObjectStore(t, nil)
	provider := &lfsTestStorage{}
	dependencies := transfers.Dependencies{Objects: objects.NewService(database), Storage: provider, Scopes: &multipartTestScope{prefix: "private-project"}, MultipartSessions: database.Store}
	first, second := transfers.NewService(dependencies), transfers.NewService(dependencies)
	key, organization, project := "new/file", "victim", "private"
	init, err := first.BeginMultipart(context.Background(), transfers.MultipartInitRequest{Key: &key, Organization: &organization, Project: &project})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.SignMultipartPart(context.Background(), init.UploadID, 1); err != nil {
		t.Fatalf("second service could not sign part: %v", err)
	}
	parts := []transfers.CompletedPart{{PartNumber: 1, ETag: "synthetic"}}
	location, err := first.CompleteMultipart(context.Background(), init.UploadID, parts)
	if err != nil {
		t.Fatal(err)
	}
	again, err := first.CompleteMultipart(context.Background(), init.UploadID, parts)
	if err != nil {
		t.Fatalf("completion retry returned error: %v", err)
	}
	if again != location {
		t.Fatalf("completion retry location = %q, want %q", again, location)
	}
	if len(provider.complete.Parts) != 1 {
		encoded, _ := json.Marshal(provider.complete)
		t.Fatalf("provider completion = %s", encoded)
	}
}
