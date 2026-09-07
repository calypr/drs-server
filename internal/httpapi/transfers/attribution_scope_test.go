package transfers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

func TestHandleInternalDownloadAmbiguousScopeSucceedsWithUnattributedEvent(t *testing.T) {
	database := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"shared-object": {
				Id: "shared-object",
				ControlledAccess: &[]string{
					"/organization/org/project/project-a",
					"/organization/org/project/project-b",
				},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/shared-object"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
	}

	response := doInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/download/shared-object", nil),
		newInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("ambiguous download status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("ambiguous download events = %+v, want one event", database.TransferEvents)
	}
	event := database.TransferEvents[0]
	if event.Organization != "" || event.Project != "" {
		t.Fatalf("ambiguous download was attributed to %q/%q", event.Organization, event.Project)
	}
}

func TestHandleInternalUploadURLUsesAuthorizedExplicitScopeForAttribution(t *testing.T) {
	database := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"scoped-object": {
				Id: "scoped-object",
				ControlledAccess: &[]string{
					"/organization/org/project/project",
					"/organization/org/project/other",
				},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/scoped-object"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
		BucketScopes: map[string]buckets.Scope{
			"org|":        {Organization: "org", Bucket: "bucket"},
			"org|project": {Organization: "org", ProjectID: "project", Bucket: "bucket"},
			"org|missing": {Organization: "org", ProjectID: "missing", Bucket: "bucket"},
		},
	}

	response := doInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/upload/scoped-object?organization=org&project=project&key=scoped-object", nil),
		newInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit upload status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("explicit upload events = %+v, want one event", database.TransferEvents)
	}
	event := database.TransferEvents[0]
	if event.Organization != "org" || event.Project != "project" {
		t.Fatalf("explicit upload scope = %q/%q, want org/project", event.Organization, event.Project)
	}

	database.TransferEvents = nil
	unsupported := doInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/upload/scoped-object?organization=org&project=missing&key=scoped-object", nil),
		newInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if unsupported.Code != http.StatusOK {
		t.Fatalf("unsupported upload scope status = %d, body = %s", unsupported.Code, unsupported.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("unsupported upload events = %+v, want one event", database.TransferEvents)
	}
	if event := database.TransferEvents[0]; event.Organization != "" || event.Project != "" {
		t.Fatalf("unsupported upload scope = %q/%q, want empty scope", event.Organization, event.Project)
	}
}
