// Package lfs owns the generated Git LFS protocol conversions.
package lfs

import (
	"strings"

	generated "github.com/calypr/syfon/apigen/server/lfsapi"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/objects"
)

// FromGeneratedCandidate converts the LFS metadata shape to the plain value
// stored by the transfer workflow. The selected fields preserve the legacy
// DRS candidate JSON written by the previous LFS adapter.
func FromGeneratedCandidate(value generated.DrsObjectCandidate) objects.Candidate {
	aliases := append([]string(nil), stringSliceValue(value.Aliases)...)
	explicitID := strings.TrimSpace(stringValue(value.Id))
	if explicitID == "" {
		for _, checksum := range checksumValues(value.Checksums) {
			if strings.EqualFold(strings.TrimSpace(checksum.Type), "sha256") {
				explicitID = syfoncommon.NormalizeOid(checksum.Checksum)
				break
			}
		}
	}
	if explicitID != "" {
		aliases = append([]string{"id:" + explicitID}, aliases...)
	}

	out := objects.Candidate{
		Aliases:     &aliases,
		Description: value.Description,
		MimeType:    value.MimeType,
		Name:        value.Name,
	}
	if value.Size != nil {
		out.Size = value.Size
	}
	if value.Checksums != nil {
		checksums := make([]objects.Checksum, 0, len(*value.Checksums))
		for _, checksum := range *value.Checksums {
			checksums = append(checksums, objects.Checksum{Type: checksum.Type, Checksum: checksum.Checksum})
		}
		if len(checksums) > 0 {
			out.Checksums = &checksums
		}
	}
	if value.AccessMethods != nil {
		methods := make([]objects.AccessMethod, 0, len(*value.AccessMethods))
		for _, method := range *value.AccessMethods {
			converted := objects.AccessMethod{AccessId: method.AccessId, Cloud: method.Region}
			if method.Type != nil {
				converted.Type = string(*method.Type)
			}
			if method.AccessUrl != nil && method.AccessUrl.Url != nil {
				converted.AccessUrl = &objects.AccessURL{Url: *method.AccessUrl.Url}
			}
			methods = append(methods, converted)
		}
		out.AccessMethods = &methods
	}
	return out
}

func checksumValues(value *[]generated.Checksum) []generated.Checksum {
	if value == nil {
		return nil
	}
	return *value
}

func stringSliceValue(value *[]string) []string {
	if value == nil {
		return nil
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
