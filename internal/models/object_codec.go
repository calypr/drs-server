package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	syfoncommon "github.com/calypr/syfon/common"
)

func (o *InternalObject) UnmarshalJSON(data []byte) error {
	*o = InternalObject{}

	// Capture the original payload so we can preserve unknown fields on marshal.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}
	if _, ok := raw["file_name"]; ok {
		return fmt.Errorf("file_name is no longer supported")
	}
	if _, ok := raw["path"]; ok {
		return fmt.Errorf("path is no longer supported")
	}
	o.Properties = raw

	type wireObject struct {
		drs.DrsObject
		Did         string            `json:"did,omitempty"`
		Hashes      map[string]string `json:"hashes,omitempty"`
		NameAliases []string          `json:"name_aliases,omitempty"`
	}

	var wire wireObject
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	if wire.Id != "" {
		o.Id = wire.Id
	} else if wire.Did != "" {
		o.Id = wire.Did
	}
	o.Checksums = append([]drs.Checksum(nil), wire.Checksums...)
	if len(o.Checksums) == 0 && len(wire.Hashes) > 0 {
		o.Checksums = make([]drs.Checksum, 0, len(wire.Hashes))
		for typ, checksum := range wire.Hashes {
			if checksum == "" {
				continue
			}
			o.Checksums = append(o.Checksums, drs.Checksum{Type: typ, Checksum: checksum})
		}
	}
	o.AccessMethods = wire.AccessMethods
	o.ControlledAccess = wire.ControlledAccess
	o.CreatedTime = wire.CreatedTime
	o.UpdatedTime = wire.UpdatedTime
	o.Size = wire.Size
	o.Name = wire.Name
	o.NameAliases = normalizeNameAliases(stringValue(wire.Name), wire.NameAliases)
	o.Description = wire.Description
	o.MimeType = wire.MimeType
	o.SelfUri = wire.SelfUri
	o.Version = wire.Version

	if wire.ControlledAccess != nil {
		o.Authorizations = syfoncommon.ControlledAccessToAuthzMap(*wire.ControlledAccess)
	}

	return nil
}

func (o InternalObject) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{})

	// Preserve original request fields, then layer canonical aliases on top.
	for k, v := range o.Properties {
		if isRetiredInternalAuthField(k) {
			continue
		}
		out[k] = v
	}

	if o.Id != "" {
		out["id"] = o.Id
	}
	if len(o.Checksums) > 0 {
		out["checksums"] = o.Checksums
	}
	if o.AccessMethods != nil {
		out["access_methods"] = o.AccessMethods
	}
	if o.ControlledAccess != nil {
		out["controlled_access"] = o.ControlledAccess
	}
	if !o.CreatedTime.IsZero() {
		out["created_time"] = o.CreatedTime.Format(time.RFC3339)
	}
	if o.UpdatedTime != nil {
		out["updated_time"] = o.UpdatedTime.Format(time.RFC3339)
	}
	if o.Name != nil {
		out["name"] = *o.Name
	}
	if len(o.NameAliases) > 0 {
		out["name_aliases"] = normalizeNameAliases(stringValue(o.Name), o.NameAliases)
	}
	if o.Description != nil {
		out["description"] = *o.Description
	}
	if o.Size > 0 {
		out["size"] = o.Size
	}
	// Ensure Gen3 compatibility fields are also present.
	out["did"] = o.Id
	if len(o.Checksums) > 0 {
		hashes := make(map[string]string, len(o.Checksums))
		for _, c := range o.Checksums {
			if c.Type == "" || c.Checksum == "" {
				continue
			}
			hashes[c.Type] = c.Checksum
		}
		if len(hashes) > 0 {
			out["hashes"] = hashes
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
	primary = cleanToBasename(primary)
	seen := make(map[string]struct{}, len(aliases)+1)
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		name := cleanToBasename(alias)
		if name == "" || name == primary {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func cleanToBasename(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	parts := strings.Split(trimmed, "/")
	base := parts[len(parts)-1]
	if base == "" {
		return trimmed
	}
	return base
}
