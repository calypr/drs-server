package objects

import (
	"context"
	clientaccess "github.com/calypr/syfon/client/access"
	clienthash "github.com/calypr/syfon/client/hash"
	"sort"
	"strings"
	"time"
)

func canonicalizeProjectScopedObjects(objects []Record, organization, project string) []Record {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}

	forcedResource := ""
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization != "" && project != "" {
		if resource, err := clientaccess.ResourcePath(organization, project); err == nil {
			forcedResource = resource
		}
	}

	grouped := make(map[string][]Record)
	passthrough := make([]Record, 0)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, forcedResource)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[key] = append(grouped[key], obj)
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]Record, 0, len(keys)+len(passthrough))
	for _, key := range keys {
		out = append(out, collapseCanonicalGroup(grouped[key]))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Id == out[j].Id {
			return canonicalObjectSortTime(out[i]).After(canonicalObjectSortTime(out[j]))
		}
		return out[i].Id < out[j].Id
	})
	return out
}

func canonicalProjectChecksumKey(obj *Record, forcedResource string) (string, bool) {
	if obj == nil {
		return "", false
	}
	sha, ok := CanonicalSHA256(obj.Checksums)
	if !ok || strings.TrimSpace(sha) == "" {
		return "", false
	}
	resource := strings.TrimSpace(forcedResource)
	if resource == "" {
		resources := AccessResources(obj)
		projectScopes := make([]string, 0, len(resources))
		for _, resource := range resources {
			org, project, ok := clientaccess.ResourceScope(resource)
			if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
				continue
			}
			projectScopes = append(projectScopes, resource)
		}
		resources = clientaccess.NormalizeAccessResources(projectScopes)
		if len(resources) != 1 {
			return "", false
		}
		resource = resources[0]
	}
	return resource + "|" + sha, true
}

func canonicalizeContentObjects(objects []Record) []Record {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}
	grouped := make(map[string][]Record)
	passthrough := make([]Record, 0)
	for _, obj := range objects {
		sha, ok := CanonicalSHA256(obj.Checksums)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[sha] = append(grouped[sha], obj)
	}
	out := make([]Record, 0, len(grouped)+len(passthrough))
	for _, group := range grouped {
		out = append(out, collapseCanonicalGroup(group))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

func objectsWithSHA256(objects []Record, checksum string) []Record {
	target := clienthash.NormalizeOid(checksum)
	if target == "" {
		return objects
	}
	matched := make([]Record, 0, len(objects))
	for _, obj := range objects {
		sha, ok := CanonicalSHA256(obj.Checksums)
		if ok && sha == target {
			matched = append(matched, obj)
		}
	}
	return matched
}

func collapseCanonicalGroup(group []Record) Record {
	if len(group) == 0 {
		return Record{}
	}
	canonical := group[0]
	latest := group[0]
	for i := 1; i < len(group); i++ {
		obj := group[i]
		created, canonicalCreated := obj.CreatedTime.UTC(), canonical.CreatedTime.UTC()
		if created.Before(canonicalCreated) || created.Equal(canonicalCreated) && obj.Id < canonical.Id {
			canonical = obj
		}
		when, latestWhen := canonicalObjectSortTime(obj), canonicalObjectSortTime(latest)
		if when.After(latestWhen) || when.Equal(latestWhen) && obj.Id > latest.Id {
			latest = obj
		}
	}

	merged := cloneObject(canonical)
	merged.Name = latest.Name
	merged.Size = pickLatestNonZeroSize(group, canonical.Size)
	merged.Description, merged.Version = pickLatestStrings(group, canonical.Description, canonical.Version)
	updated := canonicalObjectSortTime(latest)
	merged.UpdatedTime = &updated
	merged.Checksums = mergeChecksums(group)
	merged.AccessMethods = mergeAccessMethods(group)
	controlled, public := mergeControlledAccess(group)
	merged.PublicRead = public
	for _, obj := range group {
		if obj.PublicReadPolicyKnown {
			merged.PublicReadPolicyKnown = true
			break
		}
	}
	if len(controlled) > 0 {
		merged.ControlledAccess = &controlled
	} else {
		merged.ControlledAccess = nil
	}
	merged.NameAliases = mergeNameAliases(merged.Name, group)
	merged.Aliases = mergeStringPointerValues(func(obj Record) []string {
		if obj.Aliases == nil {
			return nil
		}
		return *obj.Aliases
	}, group)
	merged.SelfUri = "drs://" + string(merged.Id)
	return merged
}

func canonicalObjectSortTime(obj Record) time.Time {
	if obj.UpdatedTime != nil && !obj.UpdatedTime.IsZero() {
		return obj.UpdatedTime.UTC()
	}
	return obj.CreatedTime.UTC()
}

func cloneObjects(objects []Record) []Record {
	out := make([]Record, 0, len(objects))
	for _, obj := range objects {
		out = append(out, cloneObject(obj))
	}
	return out
}

func cloneObject(obj Record) Record {
	cloned := obj
	cloned.Checksums = append([]Checksum(nil), obj.Checksums...)
	cloned.NameAliases = append([]string(nil), obj.NameAliases...)
	if obj.AccessMethods != nil {
		methods := append([]AccessMethod(nil), (*obj.AccessMethods)...)
		cloned.AccessMethods = &methods
	}
	if obj.ControlledAccess != nil {
		controlled := append([]string(nil), (*obj.ControlledAccess)...)
		cloned.ControlledAccess = &controlled
	}
	if obj.Aliases != nil {
		aliases := append([]string(nil), (*obj.Aliases)...)
		cloned.Aliases = &aliases
	}
	return cloned
}

func mergeChecksums(group []Record) []Checksum {
	seen := make(map[string]struct{})
	merged := make([]Checksum, 0)
	for _, obj := range group {
		for _, checksum := range obj.Checksums {
			key := checksum.Type + "|" + checksum.Checksum
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, checksum)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Type == merged[j].Type {
			return merged[i].Checksum < merged[j].Checksum
		}
		return merged[i].Type < merged[j].Type
	})
	return merged
}

func mergeAccessMethods(group []Record) *[]AccessMethod {
	seen := make(map[string]struct{})
	methods := make([]AccessMethod, 0)
	for _, obj := range group {
		if obj.AccessMethods == nil {
			continue
		}
		for _, method := range *obj.AccessMethods {
			url := ""
			if method.AccessUrl != nil {
				url = method.AccessUrl.Url
			}
			key := method.Type + "|" + url
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			accessID := AccessMethodID(method.Type, url)
			method.AccessId = &accessID
			methods = append(methods, method)
		}
	}
	if len(methods) == 0 {
		return nil
	}
	sort.Slice(methods, func(i, j int) bool {
		iURL := ""
		jURL := ""
		if methods[i].AccessUrl != nil {
			iURL = methods[i].AccessUrl.Url
		}
		if methods[j].AccessUrl != nil {
			jURL = methods[j].AccessUrl.Url
		}
		if methods[i].Type == methods[j].Type {
			return iURL < jURL
		}
		return methods[i].Type < methods[j].Type
	})
	return &methods
}

func mergeControlledAccess(group []Record) ([]string, bool) {
	resources := make([]string, 0)
	public := false
	for _, obj := range group {
		objectResources := AccessResources(&obj)
		if len(objectResources) == 0 {
			public = public || obj.PublicRead || !obj.PublicReadPolicyKnown
			continue
		}
		resources = append(resources, objectResources...)
	}
	for _, obj := range group {
		public = public || obj.PublicRead
	}
	return clientaccess.NormalizeAccessResources(resources), public
}

func mergeNameAliases(primary *string, group []Record) []string {
	candidates := make([]string, 0)
	for _, obj := range group {
		if obj.Name != nil {
			candidates = append(candidates, *obj.Name)
		}
		candidates = append(candidates, obj.NameAliases...)
	}
	primaryName := ""
	if primary != nil {
		primaryName = *primary
	}
	return NormalizeNameAliases(primaryName, candidates)
}

func pickLatestNonZeroSize(group []Record, fallback int64) int64 {
	best := fallback
	var bestTime time.Time
	bestID := ""
	for _, obj := range group {
		if obj.Size <= 0 {
			continue
		}
		when := canonicalObjectSortTime(obj)
		if best <= 0 || when.After(bestTime) || when.Equal(bestTime) && string(obj.Id) > bestID {
			best = obj.Size
			bestTime = when
			bestID = string(obj.Id)
		}
	}
	return best
}

func pickLatestStrings(group []Record, description, version *string) (*string, *string) {
	var descriptionTime, versionTime time.Time
	descriptionID, versionID := "", ""
	for _, obj := range group {
		when := canonicalObjectSortTime(obj)
		id := string(obj.Id)
		if value := obj.Description; value != nil && strings.TrimSpace(*value) != "" && (description == nil || when.After(descriptionTime) || when.Equal(descriptionTime) && id > descriptionID) {
			trimmed := strings.TrimSpace(*value)
			description, descriptionTime, descriptionID = &trimmed, when, id
		}
		if value := obj.Version; value != nil && strings.TrimSpace(*value) != "" && (version == nil || when.After(versionTime) || when.Equal(versionTime) && id > versionID) {
			trimmed := strings.TrimSpace(*value)
			version, versionTime, versionID = &trimmed, when, id
		}
	}
	return description, version
}

func mergeStringPointerValues(getter func(Record) []string, group []Record) *[]string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, obj := range group {
		for _, value := range getter(obj) {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return &values
}

func (s *Service) CollapseProjectChecksumDuplicates(ctx context.Context, organization, project string) (int, error) {
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return 0, err
	}
	if err := bulkObjectMethodError(ctx, objects, objectMethodUpdate); err != nil {
		return 0, err
	}

	grouped := make(map[string][]Record)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, "")
		if !ok {
			continue
		}
		grouped[key] = append(grouped[key], obj)
	}

	merged := make([]Record, 0, len(grouped))
	aliasMap := make(map[string]string)
	toDelete := make([]string, 0)
	keys := make([]string, 0, len(grouped))
	for key, group := range grouped {
		if len(group) < 2 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := grouped[key]
		canonical := collapseCanonicalGroup(group)
		merged = append(merged, canonical)
		for _, obj := range group {
			if obj.Id == canonical.Id {
				continue
			}
			aliasMap[string(obj.Id)] = string(canonical.Id)
			toDelete = append(toDelete, string(obj.Id))
		}
	}

	if len(merged) == 0 {
		return 0, nil
	}
	if err := s.store.RegisterObjects(ctx, merged); err != nil {
		return 0, err
	}
	for aliasID, canonicalID := range aliasMap {
		if err := s.store.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
			return 0, err
		}
	}
	seen := make(map[string]struct{}, len(toDelete))
	uniqueIDs := make([]string, 0, len(toDelete))
	for _, id := range toDelete {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	sort.Strings(uniqueIDs)
	if err := s.store.BulkDeleteObjects(ctx, uniqueIDs); err != nil {
		return 0, err
	}
	return len(aliasMap), nil
}
