package models

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
)

func TestInternalObjectCodecIDSelection(t *testing.T) {
	var obj InternalObject
	if err := json.Unmarshal([]byte(`{"id":"canonical-id","did":"legacy-id"}`), &obj); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if obj.Id != "canonical-id" {
		t.Fatalf("expected id to take precedence over did, got %q", obj.Id)
	}
}

func TestInternalObjectCodecChecksumFallback(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []drs.Checksum
	}{
		{
			name:    "explicitly empty checksums use hashes",
			payload: `{"checksums":[],"hashes":{"sha256":"from-hashes"}}`,
			want:    []drs.Checksum{{Type: "sha256", Checksum: "from-hashes"}},
		},
		{
			name:    "nonempty checksums ignore hashes",
			payload: `{"checksums":[{"type":"md5","checksum":"canonical"}],"hashes":{"sha256":"legacy"}}`,
			want:    []drs.Checksum{{Type: "md5", Checksum: "canonical"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var obj InternalObject
			if err := json.Unmarshal([]byte(tt.payload), &obj); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if !reflect.DeepEqual(obj.Checksums, tt.want) {
				t.Fatalf("checksums = %+v, want %+v", obj.Checksums, tt.want)
			}
		})
	}
}

func TestInternalObjectCodecEmptyInputs(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "null", payload: "null"},
		{name: "empty object", payload: "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var obj InternalObject
			if err := json.Unmarshal([]byte(tt.payload), &obj); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if obj.Properties == nil {
				t.Fatal("expected empty input to retain a non-nil properties map")
			}
			if len(obj.Properties) != 0 || obj.Id != "" || obj.Name != nil || obj.Checksums != nil {
				t.Fatalf("unexpected zero object: %+v", obj)
			}
		})
	}
}

func TestInternalObjectCodecAliasNormalization(t *testing.T) {
	raw := []byte(`{
		"name":"/primary/primary.txt",
		"name_aliases":["\\other\\z.txt", "/other/a.txt", "/primary/primary.txt", "a.txt", "", "/again/z.txt"]
	}`)

	var obj InternalObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	want := []string{"a.txt", "z.txt"}
	if !reflect.DeepEqual(obj.NameAliases, want) {
		t.Fatalf("name aliases = %v, want %v", obj.NameAliases, want)
	}

	encoded, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode marshaled payload: %v", err)
	}
	if got, wantJSON := payload["name_aliases"], []any{"a.txt", "z.txt"}; !reflect.DeepEqual(got, wantJSON) {
		t.Fatalf("marshaled name aliases = %v, want %v", got, wantJSON)
	}
}

func TestInternalObjectCodecAlwaysEmitsDID(t *testing.T) {
	encoded, err := json.Marshal(InternalObject{})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode marshaled payload: %v", err)
	}
	value, ok := payload["did"]
	if !ok {
		t.Fatal("expected did key in empty object payload")
	}
	if value != "" {
		t.Fatalf("did = %v, want empty string", value)
	}
}
