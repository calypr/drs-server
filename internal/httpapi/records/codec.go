// Package records contains the HTTP adapter for Syfon's internal record
// representation.  The domain value remains free of generated API and JSON
// behavior; this package owns the compatibility codec.
package records

import (
	"encoding/json"
	"fmt"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

// Decode parses the internal record wire shape, including legacy did/hashes
// fields and unknown property round-tripping.
func Decode(data []byte) (objects.Record, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return objects.Record{}, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if _, ok := raw["file_name"]; ok {
		return objects.Record{}, fmt.Errorf("file_name is no longer supported")
	}
	if _, ok := raw["path"]; ok {
		return objects.Record{}, fmt.Errorf("path is no longer supported")
	}

	var wire struct {
		ID               string                  `json:"id,omitempty"`
		Did              string                  `json:"did,omitempty"`
		Checksums        []objects.Checksum      `json:"checksums,omitempty"`
		Hashes           map[string]string       `json:"hashes,omitempty"`
		AccessMethods    *[]objects.AccessMethod `json:"access_methods,omitempty"`
		ControlledAccess *[]string               `json:"controlled_access,omitempty"`
		CreatedTime      time.Time               `json:"created_time"`
		UpdatedTime      *time.Time              `json:"updated_time,omitempty"`
		Size             int64                   `json:"size,omitempty"`
		Name             *string                 `json:"name,omitempty"`
		NameAliases      []string                `json:"name_aliases,omitempty"`
		Description      *string                 `json:"description,omitempty"`
		MimeType         *string                 `json:"mime_type,omitempty"`
		SelfUri          string                  `json:"self_uri,omitempty"`
		Version          *string                 `json:"version,omitempty"`
		Aliases          *[]string               `json:"aliases,omitempty"`
		Contents         *[]objects.Content      `json:"contents,omitempty"`
		Project          string                  `json:"project,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return objects.Record{}, err
	}
	id := wire.ID
	if id == "" {
		id = wire.Did
	}
	checksums := append([]objects.Checksum(nil), wire.Checksums...)
	if len(checksums) == 0 && len(wire.Hashes) > 0 {
		checksums = make([]objects.Checksum, 0, len(wire.Hashes))
		for typ, checksum := range wire.Hashes {
			if checksum != "" {
				checksums = append(checksums, objects.Checksum{Type: typ, Checksum: checksum})
			}
		}
	}
	record := objects.Record{
		Id:               objects.RecordID(id),
		AccessMethods:    wire.AccessMethods,
		Aliases:          wire.Aliases,
		Checksums:        checksums,
		Contents:         wire.Contents,
		ControlledAccess: wire.ControlledAccess,
		CreatedTime:      wire.CreatedTime,
		Description:      wire.Description,
		MimeType:         wire.MimeType,
		Name:             wire.Name,
		NameAliases:      normalizeNameAliases(stringValue(wire.Name), wire.NameAliases),
		Project:          wire.Project,
		SelfUri:          wire.SelfUri,
		Size:             wire.Size,
		UpdatedTime:      wire.UpdatedTime,
		Version:          wire.Version,
		Properties:       raw,
	}
	if record.ControlledAccess != nil {
		record.Authorizations = clientaccess.ControlledAccessToAuthzMap(*record.ControlledAccess)
	}
	return record, nil
}

// Encode writes the compatibility record representation.  Unknown fields are
// copied first; canonical fields then override them and retired auth fields
// remain omitted.
func Encode(record objects.Record) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(record.Properties)+16)
	for key, value := range record.Properties {
		if isRetiredInternalAuthField(key) {
			continue
		}
		out[key] = value
	}
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

func isRetiredInternalAuthField(key string) bool {
	switch key {
	case "auth", "authz", "authorizations", "urls":
		return true
	default:
		return false
	}
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
