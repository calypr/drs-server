package drs

import (
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

func TestObjectPayloadIncludesLegacyIDs(t *testing.T) {
	payload := ObjectPayload(objects.Record{
		Id: "record-1",
	})
	if string(payload["id"]) != `"record-1"` || string(payload["did"]) != `"record-1"` {
		t.Fatalf("compatibility IDs = %s, %s", payload["id"], payload["did"])
	}
}
