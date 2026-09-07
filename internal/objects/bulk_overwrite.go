package objects

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
)

var ErrBulkOverwriteConflict = errors.New("bulk overwrite conflict")

// BulkOverwriteResult summarizes a project-scoped, source-wins metadata copy.
type BulkOverwriteResult struct {
	Created         int
	Replaced        int
	DIDMatched      int
	ChecksumMatched int
}

// BulkOverwriteObjects replaces records from one project snapshot without
// canonicalizing checksum siblings. A checksum can therefore exist in more
// than one project, while still identifying an existing record in this scope.
func (m *mutationService) BulkOverwriteObjects(ctx context.Context, organization, project string, candidates []Record) (BulkOverwriteResult, error) {
	var result BulkOverwriteResult
	if len(candidates) == 0 {
		return result, nil
	}
	resource, err := sycommon.ResourcePath(organization, project)
	if err != nil {
		return result, err
	}

	byDID := make(map[string]int, len(candidates))
	hashes := make([]string, 0, len(candidates))
	for i := range candidates {
		did := strings.TrimSpace(string(candidates[i].Id))
		if did == "" {
			return result, fmt.Errorf("record[%d]: did is required", i)
		}
		if _, ok := byDID[did]; ok {
			return result, fmt.Errorf("%w: duplicate source did %q", ErrBulkOverwriteConflict, did)
		}
		byDID[did] = i
		if !containsResource(AccessResources(&candidates[i]), resource) {
			return result, fmt.Errorf("record %q must include target project %s", did, resource)
		}
		if sha, ok := CanonicalSHA256(candidates[i].Checksums); ok {
			hashes = append(hashes, sha)
		}
	}

	checksumMatches, err := m.checksumScope.ListScopedObjectIDsByChecksums(ctx, organization, project, uniqueOverwriteStrings(hashes))
	if err != nil {
		return result, err
	}
	ids := make([]string, 0, len(candidates))
	for did := range byDID {
		ids = append(ids, did)
	}
	for _, matches := range checksumMatches {
		ids = append(ids, matches...)
	}
	existingList, err := m.recordReader.GetBulkObjects(ctx, uniqueOverwriteStrings(ids))
	if err != nil {
		return result, err
	}
	existing := make(map[string]Record, len(existingList))
	for _, obj := range existingList {
		existing[string(obj.Id)] = obj
	}

	resolved := make([]Record, len(candidates))
	usedTargets := make(map[string]string, len(candidates))
	for i, candidate := range candidates {
		sourceDID := string(candidate.Id)
		canonicalID, aliasErr := m.aliases.ResolveObjectAlias(ctx, sourceDID)
		if aliasErr == nil && canonicalID != sourceDID {
			return result, fmt.Errorf("%w: target DID %q is an alias for %q", ErrBulkOverwriteConflict, sourceDID, canonicalID)
		}
		if aliasErr != nil && !faults.IsNotFoundError(aliasErr) {
			return result, aliasErr
		}
		targetDID := sourceDID
		matched := false
		if current, ok := existing[sourceDID]; ok {
			if !containsResource(AccessResources(&current), resource) {
				return result, fmt.Errorf("%w: target DID %q is outside project %s", ErrBulkOverwriteConflict, sourceDID, resource)
			}
			matched = true
			result.DIDMatched++
		} else if sha, ok := CanonicalSHA256(candidate.Checksums); ok {
			matches := uniqueOverwriteStrings(checksumMatches[sha])
			switch len(matches) {
			case 0:
			case 1:
				targetDID = matches[0]
				matched = true
				result.ChecksumMatched++
			default:
				return result, fmt.Errorf("%w: target project already has multiple records for sha256 %q: %s", ErrBulkOverwriteConflict, sha, strings.Join(matches, ", "))
			}
		}
		if prior, ok := usedTargets[targetDID]; ok {
			return result, fmt.Errorf("%w: source records %q and %q resolve to target DID %q", ErrBulkOverwriteConflict, prior, sourceDID, targetDID)
		}
		usedTargets[targetDID] = sourceDID
		candidate.Id = RecordID(targetDID)
		candidate.SelfUri = "drs://" + targetDID
		resolved[i] = candidate
		if matched {
			if err := m.RequireObjectResources(ctx, objectMethodUpdate, []string{resource}); err != nil {
				return result, err
			}
			current := existing[targetDID]
			if err := requireAllObjectMethod(ctx, &current, objectMethodUpdate); err != nil {
				return result, err
			}
			if !hasObjectMethod(ctx, &candidate, objectMethodUpdate) {
				return result, faults.ErrUnauthorized
			}
			result.Replaced++
		} else {
			if err := m.RequireObjectResources(ctx, objectMethodCreate, []string{resource}); err != nil {
				return result, err
			}
			if !hasObjectMethod(ctx, &candidate, objectMethodCreate) {
				return result, faults.ErrUnauthorized
			}
			result.Created++
		}
	}

	if err := m.recordWriter.RegisterObjects(ctx, resolved); err != nil {
		return BulkOverwriteResult{}, err
	}
	return result, nil
}

func containsResource(resources []string, target string) bool {
	for _, resource := range resources {
		if strings.TrimSpace(resource) == target {
			return true
		}
	}
	return false
}

func uniqueOverwriteStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
