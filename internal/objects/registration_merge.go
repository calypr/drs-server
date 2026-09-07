package objects

import (
	"strings"
	"time"
)

// RegistrationMergeInput is the state loaded by a content registration
// transaction and the metadata supplied by the incoming registration.
// Resource slices select metadata replacement semantics; authorization remains
// the caller's responsibility.
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
