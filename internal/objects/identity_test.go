package objects

import (
	"strings"
	"testing"
)

func TestRecordIdentityHelpers(t *testing.T) {
	first, err := MintRecordIDFromChecksum("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"/organization/org/project/project"})
	if err != nil || first == "" {
		t.Fatalf("minted record id = %q/%v", first, err)
	}
	second, err := MintRecordIDFromChecksum("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"/programs/org/projects/project"})
	if err != nil || first != second {
		t.Fatalf("normalized identity mismatch: %q/%q/%v", first, second, err)
	}
	if _, err := MintRecordIDFromChecksum("", nil); err == nil {
		t.Fatal("expected missing checksum error")
	}
	if got := AccessMethodID(" S3 ", " s3://bucket/key "); got == "" {
		t.Fatal("expected access method id")
	}
}

func TestMintRecordIDFromChecksumUsesCanonicalProjectScope(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	first, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon/project/e2e"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	second, err := MintRecordIDFromChecksum(checksum, []string{"/programs/syfon/projects/e2e"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	other, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon/project/other"})
	if err != nil {
		t.Fatalf("MintRecordIDFromChecksum returned error: %v", err)
	}
	if first == "" || second == "" || other == "" {
		t.Fatalf("expected non-empty object IDs: %q %q %q", first, second, other)
	}
	if first != second {
		t.Fatalf("canonical scope IDs differ: %q and %q", first, second)
	}
	if first == other {
		t.Fatalf("scope-sensitive IDs match: %q and %q", first, other)
	}
	if _, err := MintRecordIDFromChecksum(checksum, nil); err == nil {
		t.Fatal("expected missing-scope error")
	}
	if _, err := MintRecordIDFromChecksum(checksum, []string{"/organization/syfon"}); err == nil {
		t.Fatal("expected organization-only scope error")
	}
}
