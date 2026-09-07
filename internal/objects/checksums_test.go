package objects

import "testing"

func TestChecksumHelpers(t *testing.T) {
	checksums := []Checksum{{Type: "sha-256", Checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Type: "md5", Checksum: "m"}}
	if !LooksLikeSHA256("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("expected SHA256 shape")
	}
	if LooksLikeSHA256("not-a-hash") {
		t.Fatal("unexpected SHA256 shape")
	}
	if typ, value := ParseHashQuery("sha-256:abc", ""); typ != "sha256" || value != "abc" {
		t.Fatalf("parsed hash = %q/%q", typ, value)
	}
	if value, ok := CanonicalSHA256(checksums); !ok || value != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("canonical hash = %q/%v", value, ok)
	}
	if !RecordHasChecksumTypeAndValue(Record{Checksums: checksums}, "md5", "m") {
		t.Fatal("expected md5 match")
	}
	merged := MergeAdditionalChecksums(checksums, []Checksum{{Type: "SHA256", Checksum: "other"}, {Type: "etag", Checksum: "e"}})
	if len(merged) != 3 || merged[2].Type != "etag" {
		t.Fatalf("merged checksums = %+v", merged)
	}
	if _, ok, err := ValidateCanonicalSHA256([]Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}); err == nil || ok {
		t.Fatal("expected conflicting SHA256 values")
	}
	if normalized, ok := NormalizeSHA256Query("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !ok || normalized == "" {
		t.Fatal("expected normalized SHA256 query")
	}
}
