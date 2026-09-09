package objects

import (
	"fmt"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"strings"
	"time"
)

func CandidateToRecord(c Candidate, now time.Time) (Record, error) {
	var checksums []Checksum
	if c.Checksums != nil {
		checksums = append([]Checksum(nil), (*c.Checksums)...)
	}
	oid, ok := CanonicalSHA256(checksums)
	if !ok {
		return Record{}, errorapi.ErrNoValidSHA256
	}
	if c.AccessMethods == nil || len(*c.AccessMethods) == 0 {
		return Record{}, errorapi.ErrAccessMethodsRequired
	}
	var controlled []string
	if c.ControlledAccess != nil {
		controlled = *c.ControlledAccess
	}
	controlled = clientaccess.NormalizeAccessResources(controlled)

	id := ""
	if c.Aliases != nil {
		for _, alias := range *c.Aliases {
			if strings.HasPrefix(alias, "id:") {
				id = strings.TrimPrefix(alias, "id:")
				break
			}
		}
	}
	if id == "" {
		mintedID, err := MintRecordIDFromChecksum(oid, controlled)
		if err != nil {
			return Record{}, err
		}
		id = string(mintedID)
	}

	size := int64(0)
	if c.Size != nil {
		size = *c.Size
	}
	obj := Record{
		Id:          RecordID(id),
		Size:        size,
		CreatedTime: now,
		UpdatedTime: &now,
		Version:     objectStringPtr("1"),
		MimeType:    c.MimeType,
		Description: c.Description,
		Aliases:     c.Aliases,
		Checksums:   []Checksum{{Type: "sha256", Checksum: oid}},
	}
	if c.ControlledAccess != nil {
		obj.ControlledAccess = &controlled
	}
	if c.Name != nil {
		name := CleanToBasename(*c.Name)
		if name != "" {
			obj.Name = objectStringPtr(name)
		}
	}
	if obj.Name == nil || strings.TrimSpace(*obj.Name) == "" {
		obj.Name = &oid
	}
	obj.SelfUri = "drs://" + string(obj.Id)

	methods := make([]AccessMethod, 0, len(*c.AccessMethods))
	for _, method := range *c.AccessMethods {
		if method.AccessId == nil || *method.AccessId == "" {
			method.AccessId = objectStringPtr(method.Type)
		}
		methods = append(methods, method)
	}
	obj.AccessMethods = &methods
	if len(methods) == 0 {
		return Record{}, errorapi.ErrAccessMethodsRequired
	}
	return obj, nil
}

func EnforceCanonicalProjectScope(obj Record, organization, project string) (Record, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if project != "" && organization == "" {
		return Record{}, fmt.Errorf("organization is required when project is set")
	}
	if organization == "" || project == "" {
		return obj, nil
	}

	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return Record{}, err
	}
	controlled := append(AccessResources(&obj), resource)
	controlled = clientaccess.NormalizeAccessResources(controlled)
	obj.ControlledAccess = &controlled
	return obj, nil
}

// MergeRecordUpdate applies the mutable fields from update while retaining
// immutable identity and existing fields that were omitted by the caller.
func MergeRecordUpdate(existing Record, update Record, id string, now time.Time) (Record, error) {
	merged := existing
	merged.Id = RecordID(id)
	merged.UpdatedTime = &now
	if update.Name != nil {
		name := CleanToBasename(*update.Name)
		if name == "" {
			merged.Name = nil
		} else {
			merged.Name = objectStringPtr(name)
		}
	}
	if update.Description != nil {
		merged.Description = update.Description
	}
	if update.MimeType != nil {
		merged.MimeType = update.MimeType
	}
	if update.Version != nil {
		merged.Version = update.Version
	}
	if update.Aliases != nil {
		merged.Aliases = update.Aliases
	}
	if update.ControlledAccess != nil {
		merged.ControlledAccess = update.ControlledAccess
	}
	if update.AccessMethods != nil {
		merged.AccessMethods = update.AccessMethods
	}
	if update.Checksums != nil {
		merged.Checksums = MergeAdditionalChecksums(existing.Checksums, update.Checksums)
	}

	return merged, nil
}

type RegistrationMergeInput struct {
	ExistingName        string
	ExistingVersion     string
	ExistingDescription string
	ExistingSize        int64
	ExistingUpdated     time.Time
	IncomingName        string
	IncomingVersion     string
	IncomingDescription string
	IncomingSize        int64
	IncomingUpdated     time.Time
	IncomingResources   []string
	CurrentResources    []string
}

// RegistrationMergeResult contains the merged metadata and, when needed, the
// name alias that must be inserted by the adapter. It is deliberately free of
// SQL, context, and authorization side effects.
type RegistrationMergeResult struct {
	Name        string
	Version     string
	Description string
	Size        int64
	Updated     time.Time
	NameAlias   string
}

// MergeRegistrationMetadata applies registration-specific merge semantics.
// This is intentionally separate from MergeRecordUpdate: registration may
// replace metadata only for one overlapping current resource, while ordinary
// record updates have different field and authorization semantics.
func MergeRegistrationMetadata(input RegistrationMergeInput) RegistrationMergeResult {
	allowReplacement := len(input.CurrentResources) == 1 && hasRegistrationResourceOverlap(input.IncomingResources, input.CurrentResources)
	incomingName := CleanToBasename(input.IncomingName)

	result := RegistrationMergeResult{
		Name:        input.ExistingName,
		Version:     input.ExistingVersion,
		Description: input.ExistingDescription,
		Size:        input.ExistingSize,
		Updated:     input.ExistingUpdated,
	}
	if input.ExistingName != "" && incomingName != "" && input.ExistingName != incomingName {
		result.NameAlias = incomingName
		if allowReplacement {
			result.NameAlias = input.ExistingName
		}
	}
	if allowReplacement || strings.TrimSpace(result.Name) == "" {
		if incomingName != "" {
			result.Name = incomingName
		}
	}
	if allowReplacement || strings.TrimSpace(result.Version) == "" {
		if incoming := strings.TrimSpace(input.IncomingVersion); incoming != "" {
			result.Version = incoming
		}
	}
	if allowReplacement || strings.TrimSpace(result.Description) == "" {
		if incoming := strings.TrimSpace(input.IncomingDescription); incoming != "" {
			result.Description = incoming
		}
	}
	if result.Size == 0 && input.IncomingSize != 0 {
		result.Size = input.IncomingSize
	}
	if input.IncomingUpdated.After(result.Updated) {
		result.Updated = input.IncomingUpdated
	}
	return result
}

func hasRegistrationResourceOverlap(left, right []string) bool {
	set := make(map[string]struct{}, len(left))
	for _, resource := range left {
		set[resource] = struct{}{}
	}
	for _, resource := range right {
		if _, ok := set[resource]; ok {
			return true
		}
	}
	return false
}

func objectStringPtr(value string) *string { return &value }
