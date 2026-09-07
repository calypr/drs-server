package drs

import (
	"encoding/json"
	"testing"

	"github.com/calypr/syfon/internal/objects"
)

func TestToGeneratedChecksumNilAndEmptySlicesRemainDistinct(t *testing.T) {
	nilValue := ToGenerated(objects.Record{})
	if nilValue.Checksums != nil {
		t.Fatalf("nil domain checksums became empty generated slice: %#v", nilValue.Checksums)
	}
	emptyValue := ToGenerated(objects.Record{Checksums: []objects.Checksum{}})
	if emptyValue.Checksums == nil || len(emptyValue.Checksums) != 0 {
		t.Fatalf("empty domain checksums changed: %#v", emptyValue.Checksums)
	}
}

func TestObjectPayloadIncludesLegacyIDAndPreservesUnknownJSON(t *testing.T) {
	payload := ObjectPayload(objects.Record{
		Id: "record-1",
		Properties: map[string]json.RawMessage{
			"did":       json.RawMessage(`"stale"`),
			"file_name": json.RawMessage(`"legacy-name"`),
			"large":     json.RawMessage(`9007199254740993`),
			"path":      json.RawMessage(`"legacy/path"`),
		},
	})
	if string(payload["id"]) != `"record-1"` || string(payload["did"]) != `"record-1"` {
		t.Fatalf("compatibility IDs = %s, %s", payload["id"], payload["did"])
	}
	for key, want := range map[string]string{"file_name": `"legacy-name"`, "large": `9007199254740993`, "path": `"legacy/path"`} {
		if got := string(payload[key]); got != want {
			t.Fatalf("%s changed: got %s want %s", key, got, want)
		}
	}
}
