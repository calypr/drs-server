package records

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/objects"
)

func TestCodecIDAndChecksumCompatibility(t *testing.T) {
	got, err := Decode([]byte(`{"id":"canonical-id","did":"legacy-id","checksums":[],"hashes":{"sha256":"from-hashes"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != "canonical-id" || !reflect.DeepEqual(got.Checksums, []objects.Checksum{{Type: "sha256", Checksum: "from-hashes"}}) {
		t.Fatalf("decoded record = %+v", got)
	}
	encoded, err := Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != "canonical-id" || payload["did"] != "canonical-id" {
		t.Fatalf("id aliases = %#v", payload)
	}
	if _, ok := payload["hashes"]; !ok {
		t.Fatal("expected hashes compatibility field")
	}
}

func TestCodecPreservesWireContractAndUnknownFields(t *testing.T) {
	raw := []byte(`{
		"id":"record-1","name":"dir/file.txt","name_aliases":["\\other\\other.txt","dir/file.txt"],
		"mime_type":"text/plain","self_uri":"drs://record-1","version":"v1","aliases":["legacy"],
		"contents":[{"name":"bundle","contents":[{"id":"child","name":"child.txt"}]}],
		"access_methods":[{"type":"s3","access_id":"read","access_url":{"url":"s3://bucket/key","headers":["x-test"]},"authorizations":{"supported_types":["bearer"]}}],
		"controlled_access":["/organization/org/project/p"],"extra":{"keep":true},"auth":"retired","path":"ignored"
	}`)
	if _, err := Decode(raw); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatal("expected retired path rejection")
	}
	raw = []byte(strings.Replace(string(raw), `,"path":"ignored"`, "", 1))
	record, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if record.MimeType == nil || *record.MimeType != "text/plain" || record.SelfUri != "drs://record-1" || record.Version == nil || *record.Version != "v1" {
		t.Fatalf("wire fields lost: %+v", record)
	}
	if !reflect.DeepEqual(record.NameAliases, []string{"other.txt"}) || record.Properties["extra"] == nil {
		t.Fatalf("aliases/properties lost: %+v", record)
	}
	if record.Contents == nil || (*record.Contents)[0].Contents == nil || (*(*record.Contents)[0].Contents)[0].Id == nil {
		t.Fatalf("nested contents lost: %+v", record.Contents)
	}
	if record.AccessMethods == nil || (*record.AccessMethods)[0].Authorizations == nil {
		t.Fatalf("access authorizations lost: %+v", record.AccessMethods)
	}
	encoded, err := Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"auth", "authz", "authorizations", "urls", "file_name", "path"} {
		if _, ok := payload[retired]; ok {
			t.Fatalf("retired field %q emitted", retired)
		}
	}
	if payload["mime_type"] != "text/plain" || payload["self_uri"] != "drs://record-1" || payload["version"] != "v1" {
		t.Fatalf("wire compatibility fields missing: %#v", payload)
	}
}

func TestCodecRejectsLegacyFileAliases(t *testing.T) {
	for _, field := range []string{"file_name", "path"} {
		if _, err := Decode([]byte(`{"` + field + `":"x"}`)); err == nil {
			t.Fatalf("expected %s to be rejected", field)
		}
	}
}

func TestCodecChecksumsTakePrecedenceOverHashes(t *testing.T) {
	record, err := Decode([]byte(`{"checksums":[{"type":"sha256","checksum":"explicit"}],"hashes":{"sha256":"legacy"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.Checksums, []objects.Checksum{{Type: "sha256", Checksum: "explicit"}}) {
		t.Fatalf("checksums precedence lost: %+v", record.Checksums)
	}
}

func TestCodecPreservesRawUnknownNumbersAndManualLegacyProperties(t *testing.T) {
	record := objects.Record{Properties: map[string]json.RawMessage{
		"large":     json.RawMessage(`9007199254740993`),
		"file_name": json.RawMessage(`"legacy.txt"`),
		"path":      json.RawMessage(`"legacy/path"`),
		"auth":      json.RawMessage(`{"retired":true}`),
	}}
	encoded, err := Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"large":9007199254740993`) {
		t.Fatalf("raw number changed: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"file_name":"legacy.txt"`) || !strings.Contains(string(encoded), `"path":"legacy/path"`) {
		t.Fatalf("manual legacy properties should match MarshalJSON behavior: %s", encoded)
	}
	if strings.Contains(string(encoded), `"auth"`) {
		t.Fatalf("retired auth property emitted: %s", encoded)
	}
}

func TestCodecNullAndEmptyPayloads(t *testing.T) {
	for _, payload := range []string{"null", "{}", `{"checksums":[],"hashes":{}}`} {
		record, err := Decode([]byte(payload))
		if err != nil {
			t.Fatalf("Decode(%s): %v", payload, err)
		}
		if record.Id != "" {
			t.Fatalf("Decode(%s) id = %q", payload, record.Id)
		}
	}
}
