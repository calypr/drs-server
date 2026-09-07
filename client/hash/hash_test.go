package hash

import (
	"encoding/json"
	"testing"
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

	checksums := []Checksum{{Type: "sha256", Checksum: "abc"}, {Type: "md5", Checksum: "def"}}
	if got := ConvertChecksumsToMap(checksums); got["sha256"] != "abc" || got["md5"] != "def" {
		t.Fatalf("unexpected checksum map: %+v", got)
	}
	if got := ConvertChecksumsToHashInfo(checksums); got != (HashInfo{MD5: "def", SHA256: "abc"}) {
		t.Fatalf("unexpected checksum hash info: %+v", got)
	}
}


