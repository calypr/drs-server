package models

import (
	"encoding/json"
	"testing"
)

func TestInternalObjectRejectsLegacyPathAliases(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "file_name", field: "file_name"},
		{name: "path", field: "path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(`{"` + tt.field + `":"legacy-value"}`)
			var obj InternalObject
			if err := json.Unmarshal(raw, &obj); err == nil {
				t.Fatalf("expected %s payload to be rejected", tt.field)
			}
		})
	}
}

func TestInternalObjectExternal(t *testing.T) {
	obj := InternalObject{}
	obj.Id = "did-2"
	external := obj.External()
	if external.Id != "did-2" {
		t.Fatalf("expected external object id did-2, got %q", external.Id)
	}
}
