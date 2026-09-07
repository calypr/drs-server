package common

import (
	"strings"
	"testing"
)

func TestMintObjectIDFromChecksum(t *testing.T) {
	valid := strings.Repeat("a", 64)
	id1, err := MintObjectIDFromChecksum(valid, []string{"/organization/syfon/project/e2e"})
	if err != nil {
		t.Fatalf("MintObjectIDFromChecksum returned error: %v", err)
	}
	id2, err := MintObjectIDFromChecksum(valid, []string{"/programs/syfon/projects/e2e"})
	if err != nil {
		t.Fatalf("MintObjectIDFromChecksum returned error: %v", err)
	}
	id3, err := MintObjectIDFromChecksum(valid, []string{"/organization/syfon/project/other"})
	if err != nil {
		t.Fatalf("MintObjectIDFromChecksum returned error: %v", err)
	}
	if id1 == "" || id2 == "" || id3 == "" {
		t.Fatalf("expected non-empty object ids: %q %q %q", id1, id2, id3)
	}
	if id1 != id2 {
		t.Fatalf("expected normalized-scope deterministic ids, got %q and %q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("expected scope-sensitive object ids, got %q and %q", id1, id3)
	}
	if _, err := MintObjectIDFromChecksum(valid, nil); err == nil {
		t.Fatal("expected missing-scope error")
	}
	if _, err := MintObjectIDFromChecksum(valid, []string{"/organization/syfon"}); err == nil {
		t.Fatal("expected org-only scope error")
	}
}
