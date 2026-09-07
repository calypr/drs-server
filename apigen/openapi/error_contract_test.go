package openapi

import (
	"strings"
	"testing"
)

func TestServiceSpecsReferenceSharedErrorContract(t *testing.T) {
	tests := []struct {
		file   string
		schema string
	}{
		{file: "openapi.yaml", schema: "Error"},
		{file: "internal.openapi.yaml", schema: "APIError"},
		{file: "bucket.openapi.yaml", schema: "APIError"},
		{file: "metrics.openapi.yaml", schema: "APIError"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			raw, err := ReadSpec(tt.file)
			if err != nil {
				t.Fatalf("read spec: %v", err)
			}
			definition := "    " + tt.schema + ":\n      $ref: './error.openapi.yaml#/components/schemas/APIError'"
			if !strings.Contains(string(raw), definition) {
				t.Fatalf("%s must reference the shared API error schema", tt.schema)
			}
		})
	}
}
