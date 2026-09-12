package hash

import (
	"encoding/json"
	"strings"
	"testing"

	drsapi "github.com/calypr/syfon/apigen/drs"
)

func TestHashInfoUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("null", func(t *testing.T) {
		var got HashInfo
		if err := json.Unmarshal([]byte("null"), &got); err != nil {
			t.Fatalf("unmarshal null: %v", err)
		}
		if got != (HashInfo{}) {
			t.Fatalf("expected zero hash info, got %+v", got)
		}
	})

	t.Run("map payload", func(t *testing.T) {
		payload := []byte(`{"md5":"m","sha1":"s1","sha256":"s256","sha512":"s512","crc32c":"crc","etag":"etag","ignored":"skip"}`)
		var got HashInfo
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal map payload: %v", err)
		}
		want := HashInfo{MD5: "m", SHA: "s1", SHA256: "s256", SHA512: "s512", CRC: "crc", ETag: "etag"}
		if got != want {
			t.Fatalf("unexpected hash info: got %+v want %+v", got, want)
		}
	})

	t.Run("checksum array payload", func(t *testing.T) {
		payload := []byte(`[{"type":"sha256","checksum":"abc"},{"type":"md5","checksum":"def"}]`)
		var got HashInfo
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal array payload: %v", err)
		}
		want := HashInfo{MD5: "def", SHA256: "abc"}
		if got != want {
			t.Fatalf("unexpected hash info: got %+v want %+v", got, want)
		}
	})

	t.Run("checksum aliases", func(t *testing.T) {
		payload := []byte(`[{"type":"SHA-256","checksum":"hyphen"},{"type":"sha256","checksum":"canonical"}]`)
		var got HashInfo
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal checksum aliases: %v", err)
		}
		if got.SHA256 != "canonical" {
			t.Fatalf("SHA-256 alias precedence = %q, want canonical value", got.SHA256)
		}

		payload = []byte(`{"SHA-256":"hyphen","sha256":"canonical"}`)
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal map aliases: %v", err)
		}
		if got.SHA256 != "canonical" {
			t.Fatalf("map SHA-256 alias precedence = %q, want canonical value", got.SHA256)
		}
	})

	t.Run("unsupported payload", func(t *testing.T) {
		var got HashInfo
		err := json.Unmarshal([]byte(`123`), &got)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestHashConversions(t *testing.T) {
	t.Parallel()

	drsChecksums := []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}
	if got := ConvertDrsChecksumsToHashInfo(drsChecksums); got.SHA256 != "abc" {
		t.Fatalf("unexpected DRS checksum conversion: %+v", got)
	}
	if got := NormalizeChecksumType(" SHA "); got != ChecksumTypeSHA1 || got.String() != "sha1" {
		t.Fatalf("unexpected checksum type normalization: %q", got)
	}
	for _, test := range []struct {
		raw  string
		want ChecksumType
	}{
		{raw: "sha-256", want: ChecksumTypeSHA256},
		{raw: " SHA-256 ", want: ChecksumTypeSHA256},
		{raw: "SHA256", want: ChecksumTypeSHA256},
		{raw: "sha-512", want: ChecksumTypeSHA512},
		{raw: "SHA-1", want: ChecksumTypeSHA1},
	} {
		if got := NormalizeChecksumType(test.raw); got != test.want {
			t.Fatalf("NormalizeChecksumType(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
	const value = "abcdef"
	if got := ConvertDrsChecksumsToHashInfo([]drsapi.Checksum{{Type: "sha-256", Checksum: value}}); got.SHA256 != value {
		t.Fatalf("sha-256 conversion = %+v, want SHA256 %q", got, value)
	}
	if got := ConvertDrsChecksumsToHashInfo([]drsapi.Checksum{{Type: "sha-256", Checksum: "alias"}, {Type: "sha256", Checksum: value}}); got.SHA256 != value {
		t.Fatalf("duplicate alias conversion = %+v, want canonical value %q", got, value)
	}
}

func TestNormalizeOid(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if got := NormalizeOid("  sha256:" + strings.ToUpper(valid) + "  "); got != valid {
		t.Fatalf("unexpected normalized oid: %q", got)
	}
	if got := NormalizeOid("not-a-valid-oid"); got != "" {
		t.Fatalf("expected invalid oid to normalize to empty string, got %q", got)
	}
}

func TestNormalizeChecksum(t *testing.T) {
	if got := NormalizeChecksum("  sha256:ABC123  "); got != "ABC123" {
		t.Fatalf("unexpected normalized checksum: %q", got)
	}
}
