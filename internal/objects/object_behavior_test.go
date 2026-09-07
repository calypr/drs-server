package objects

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
)

func TestCanonicalContentMetadataIsDeterministicOnTimestampTie(t *testing.T) {
	created := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	lowName := "low"
	highName := "high"
	lowDescription := "low description"
	highDescription := "high description"
	low := Record{Id: "uuid-a", Name: &lowName, Description: &lowDescription, Size: 1, CreatedTime: created, Checksums: []Checksum{{Type: "sha256", Checksum: strings.Repeat("f", 64)}}}
	high := Record{Id: "uuid-b", Name: &highName, Description: &highDescription, Size: 2, CreatedTime: created, Checksums: low.Checksums}

	forward := canonicalizeContentObjects([]Record{low, high})
	reverse := canonicalizeContentObjects([]Record{high, low})
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("canonical metadata depends on input order: forward=%+v reverse=%+v", forward, reverse)
	}
	if len(forward) != 1 || forward[0].Id != low.Id || forward[0].Size != high.Size || forward[0].Description == nil || *forward[0].Description != highDescription {
		t.Fatalf("expected stable uuid-a identity and deterministic latest metadata: %+v", forward)
	}
}

func TestSearchAfterID(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	if got := searchAfterID(ids, "b"); got != 2 {
		t.Fatalf("expected index 2 for startAfter=b, got %d", got)
	}
	if got := searchAfterID(ids, "bb"); got != 2 {
		t.Fatalf("expected index 2 for startAfter=bb, got %d", got)
	}
	if got := searchAfterID(ids, "z"); got != len(ids) {
		t.Fatalf("expected index %d for startAfter=z, got %d", len(ids), got)
	}
}

func TestObjectMatchesScope(t *testing.T) {
	obj := &Record{Authorizations: map[string][]string{"org1": {"p1", "p2"}}}
	if !objectMatchesScope(obj, "org1", "p1") {
		t.Fatalf("expected org1/p1 to match")
	}
	if objectMatchesScope(obj, "org1", "p3") {
		t.Fatalf("expected org1/p3 not to match")
	}
	if !objectMatchesScope(obj, "org1", "") {
		t.Fatalf("expected org-wide org1 to match")
	}
}

func TestReadableChecksumFilter(t *testing.T) {
	service := NewService(Dependencies{})
	res, includeUnscoped, restrict, ok := service.readableChecksumFilter(context.Background(), "", "")
	if !ok || includeUnscoped || restrict || res != nil {
		t.Fatalf("unexpected unenforced filter: res=%+v includeUnscoped=%v restrict=%v ok=%v", res, includeUnscoped, restrict, ok)
	}

	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{}, true)
	forbiddenCtx := access.WithSession(context.Background(), session)
	res, includeUnscoped, restrict, ok = service.readableChecksumFilter(forbiddenCtx, "", "")
	if !ok || !includeUnscoped || !restrict {
		t.Fatalf("expected restricted filter under enforced authz, got res=%+v includeUnscoped=%v restrict=%v ok=%v", res, includeUnscoped, restrict, ok)
	}
}
