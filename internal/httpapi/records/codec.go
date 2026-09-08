// Package records contains the HTTP adapter for Syfon's internal record
// representation.  The domain value remains free of generated API and JSON
// behavior; this package owns the compatibility codec.
package records

import (
	"encoding/json"
	"time"

	"github.com/calypr/syfon/internal/objects"
)

// Encode writes the compatibility record representation. Retired extension
// fields remain omitted at this wire boundary.
func Encode(record objects.Record) ([]byte, error) {
	out := make(map[string]json.RawMessage, 16)
	put := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out[key] = encoded
		return nil
	}
	if record.Id != "" {
		if err := put("id", string(record.Id)); err != nil {
			return nil, err
		}
	}
	if len(record.Checksums) > 0 {
		if err := put("checksums", record.Checksums); err != nil {
			return nil, err
		}
	}
	if record.AccessMethods != nil {
		if err := put("access_methods", record.AccessMethods); err != nil {
			return nil, err
		}
	}
	if record.ControlledAccess != nil {
		if err := put("controlled_access", record.ControlledAccess); err != nil {
			return nil, err
		}
	}
	if !record.CreatedTime.IsZero() {
		if err := put("created_time", record.CreatedTime.Format(time.RFC3339)); err != nil {
			return nil, err
		}
	}
	if record.UpdatedTime != nil {
		if err := put("updated_time", record.UpdatedTime.Format(time.RFC3339)); err != nil {
			return nil, err
		}
	}
	if record.Name != nil {
		if err := put("name", *record.Name); err != nil {
			return nil, err
		}
	}
	if len(record.NameAliases) > 0 {
		if err := put("name_aliases", normalizeNameAliases(stringValue(record.Name), record.NameAliases)); err != nil {
			return nil, err
		}
	}
	if record.Description != nil {
		if err := put("description", *record.Description); err != nil {
			return nil, err
		}
	}
	if record.Size > 0 {
		if err := put("size", record.Size); err != nil {
			return nil, err
		}
	}
	if err := put("did", string(record.Id)); err != nil {
		return nil, err
	}
	if len(record.Checksums) > 0 {
		hashes := make(map[string]string, len(record.Checksums))
		for _, checksum := range record.Checksums {
			if checksum.Type != "" && checksum.Checksum != "" {
				hashes[checksum.Type] = checksum.Checksum
			}
		}
		if len(hashes) > 0 {
			if err := put("hashes", hashes); err != nil {
				return nil, err
			}
		}
	}
	return json.Marshal(out)
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func normalizeNameAliases(primary string, aliases []string) []string {
	return objects.NormalizeNameAliases(primary, aliases)
}
