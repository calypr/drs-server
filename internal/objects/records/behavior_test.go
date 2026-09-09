package records

import (
	"reflect"
	"strings"
	"testing"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"
)

func TestCanonicalRecordMetadataIsDeterministicOnTimestampTie(t *testing.T) {
	created := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	lowName := "low"
	highName := "high"
	lowDescription := "low description"
	highDescription := "high description"
	low := objectmodel.Record{Id: "uuid-a", Name: &lowName, Description: &lowDescription, Size: 1, CreatedTime: created, Checksums: []objectmodel.Checksum{{Type: "sha256", Checksum: strings.Repeat("f", 64)}}}
	high := objectmodel.Record{Id: "uuid-b", Name: &highName, Description: &highDescription, Size: 2, CreatedTime: created, Checksums: low.Checksums}

	forward := canonicalizeContentObjects([]objectmodel.Record{low, high})
	reverse := canonicalizeContentObjects([]objectmodel.Record{high, low})
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("canonical metadata depends on input order: forward=%+v reverse=%+v", forward, reverse)
	}
	if len(forward) != 1 || forward[0].Id != low.Id || forward[0].Size != high.Size || forward[0].Description == nil || *forward[0].Description != highDescription {
		t.Fatalf("expected stable uuid-a identity and deterministic latest metadata: %+v", forward)
	}
}
