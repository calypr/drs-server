package records

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func TestProjectGetParity(t *testing.T) {
	name := "file"
	blank := ""
	now := time.Date(2026, 9, 8, 1, 2, 3, 123456789, time.UTC)
	bad := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	emptyStrings := []string{}
	var nilStrings []string
	methods := []objects.AccessMethod{{
		Type: "custom",
		AccessUrl: &objects.AccessURL{
			Url:     "s3://b/k",
			Headers: &nilStrings,
		},
		Authorizations: &objects.AccessAuthorizations{SupportedTypes: &nilStrings},
	}}
	emptyAliases := []string{}
	updatedBad := bad.Format(time.RFC3339)

	tests := []struct {
		name   string
		record objects.Record
		want   getResponse
	}{
		{name: "zero record", record: objects.Record{}, want: getResponse{DID: ""}},
		{name: "negative size", record: objects.Record{Id: "id", Size: -1}, want: getResponse{ID: "id", DID: "id"}},
		{
			name: "known scalar fields",
			record: objects.Record{
				Id: "id", Size: 1, CreatedTime: now, UpdatedTime: &now,
				Name: &name, Description: &blank,
			},
			want: getResponse{
				ID: "id", DID: "id", Created: now.Format(time.RFC3339), Updated: ptr(now.Format(time.RFC3339)),
				Name: &name, Description: &blank, Size: 1,
			},
		},
		{name: "empty checksums", record: objects.Record{Checksums: []objects.Checksum{}}, want: getResponse{DID: ""}},
		{
			name:   "checksums and hashes",
			record: objects.Record{Checksums: []objects.Checksum{{Type: "sha256", Checksum: "abc"}, {Type: "md5", Checksum: "x"}}},
			want: getResponse{
				DID: "", Checksums: []objects.Checksum{{Type: "sha256", Checksum: "abc"}, {Type: "md5", Checksum: "x"}},
				Hashes: map[string]string{"sha256": "abc", "md5": "x"},
			},
		},
		{
			name:   "incomplete checksums",
			record: objects.Record{Checksums: []objects.Checksum{{}, {Type: "sha256"}}},
			want:   getResponse{Checksums: []objects.Checksum{{}, {Type: "sha256"}}},
		},
		{
			name:   "aliases normalize to empty array",
			record: objects.Record{Name: &name, NameAliases: []string{"file", "file"}},
			want:   getResponse{Name: &name, NameAliases: &emptyAliases},
		},
		{
			name:   "empty pointers",
			record: objects.Record{NameAliases: emptyStrings, ControlledAccess: &emptyStrings, Aliases: &nilStrings},
			want:   getResponse{ControlledAccess: &emptyStrings},
		},
		{
			name:   "access methods and invalid created time",
			record: objects.Record{AccessMethods: &methods, CreatedTime: bad},
			want:   getResponse{AccessMethods: &methods, Created: bad.Format(time.RFC3339)},
		},
		{
			name:   "empty access methods and invalid updated time",
			record: objects.Record{AccessMethods: &[]objects.AccessMethod{}, UpdatedTime: &bad},
			want:   getResponse{AccessMethods: &[]objects.AccessMethod{}, Updated: &updatedBad},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectGet(tt.record)
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(tt.want)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, gotJSON, wantJSON)
		})
	}
}

func TestHandleInternalGetOmitsRetiredFields(t *testing.T) {
	name := "file.txt"
	version := "1"
	mimeType := "text/plain"
	aliases := []string{"alias"}
	contents := []objects.Content{{Name: "nested"}}
	store := &internalRecordStore{Objects: map[string]*objects.Record{
		"object-id": {
			Id:                    "object-id",
			Name:                  &name,
			Version:               &version,
			MimeType:              &mimeType,
			Aliases:               &aliases,
			Contents:              &contents,
			Project:               "project",
			SelfUri:               "s3://bucket/object-id",
			PublicRead:            true,
			PublicReadPolicyKnown: true,
		},
	}}
	fixture := newInternalDRSObjectManager(store)
	app := fiber.New()
	app.Get("/index/:id", handleInternalGetFiber(fixture.ObjectService))

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/index/object-id", nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"project", "version", "mime_type", "aliases", "contents", "self_uri", "public_read", "properties", "file_name", "path"} {
		if _, ok := payload[field]; ok {
			t.Errorf("retired or unknown field %q was emitted", field)
		}
	}
	if string(payload["did"]) != `"object-id"` {
		t.Fatalf("did = %s, want object-id", payload["did"])
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
