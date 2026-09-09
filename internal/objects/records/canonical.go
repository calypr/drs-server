package records

import (
	clientaccess "github.com/calypr/syfon/client/access"
	clienthash "github.com/calypr/syfon/client/hash"
	objectmodel "github.com/calypr/syfon/internal/objects"
	"sort"
	"strings"
	"time"
)

func recordStringPtr(value string) *string { return &value }

func canonicalizeProjectScopedObjects(objects []objectmodel.Record, organization, project string) []objectmodel.Record {
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

	grouped := make(map[string][]objectmodel.Record)
	passthrough := make([]objectmodel.Record, 0)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, forcedResource)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[key] = append(grouped[key], cloneObject(obj))
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]objectmodel.Record, 0, len(keys)+len(passthrough))
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

func canonicalProjectChecksumKey(obj *objectmodel.Record, forcedResource string) (string, bool) {
	if obj == nil {
		return "", false
	}
	sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
	if !ok || strings.TrimSpace(sha) == "" {
		return "", false
	}
	resource := strings.TrimSpace(forcedResource)
	if resource == "" {
		resources := projectScopeResources(obj)
		if len(resources) != 1 {
			return "", false
		}
		resource = resources[0]
	}
	return resource + "|" + sha, true
}

func projectScopeResources(obj *objectmodel.Record) []string {
	resources := objectmodel.AccessResources(obj)
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
			continue
		}
		out = append(out, resource)
	}
	return clientaccess.NormalizeAccessResources(out)
}

func canonicalizeContentObjects(objects []objectmodel.Record) []objectmodel.Record {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}
	grouped := make(map[string][]objectmodel.Record)
	passthrough := make([]objectmodel.Record, 0)
	for _, obj := range objects {
		sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[sha] = append(grouped[sha], cloneObject(obj))
	}
	out := make([]objectmodel.Record, 0, len(grouped)+len(passthrough))
	for _, group := range grouped {
		out = append(out, collapseCanonicalGroup(group))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

func objectsWithSHA256(objects []objectmodel.Record, checksum string) []objectmodel.Record {
	target := clienthash.NormalizeOid(checksum)
	if target == "" {
		return objects
	}
	matched := make([]objectmodel.Record, 0, len(objects))
	for _, obj := range objects {
		sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
		if ok && sha == target {
			matched = append(matched, obj)
		}
	}
	return matched
}

func collapseCanonicalGroup(group []objectmodel.Record) objectmodel.Record {
	if len(group) == 0 {
		return objectmodel.Record{}
	}
	canonical := cloneObject(group[0])
	latest := cloneObject(group[0])
	for i := 1; i < len(group); i++ {
		obj := cloneObject(group[i])
		if canonicalObjectOlder(obj, canonical) {
			canonical = obj
		}
		if canonicalObjectNewer(obj, latest) {
			latest = obj
		}
	}

	merged := cloneObject(canonical)
	merged.Name = latest.Name
	merged.Size = pickLatestNonZeroSize(group, canonical.Size)
	merged.Description = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.Description }, canonical.Description)
	merged.MimeType = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.MimeType }, canonical.MimeType)
	merged.Version = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.Version }, canonical.Version)
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
	merged.Aliases = mergeStringPointerValues(func(obj objectmodel.Record) []string {
		if obj.Aliases == nil {
			return nil
		}
		return *obj.Aliases
	}, group)
	merged.SelfUri = "drs://" + string(merged.Id)
	return merged
}

func canonicalObjectOlder(a, b objectmodel.Record) bool {
	at := a.CreatedTime.UTC()
	bt := b.CreatedTime.UTC()
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.Id < b.Id
}

func canonicalObjectNewer(a, b objectmodel.Record) bool {
	at := canonicalObjectSortTime(a)
	bt := canonicalObjectSortTime(b)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.Id > b.Id
}

func canonicalObjectSortTime(obj objectmodel.Record) time.Time {
	if obj.UpdatedTime != nil && !obj.UpdatedTime.IsZero() {
		return obj.UpdatedTime.UTC()
	}
	return obj.CreatedTime.UTC()
}

func cloneObjects(objects []objectmodel.Record) []objectmodel.Record {
	out := make([]objectmodel.Record, 0, len(objects))
	for _, obj := range objects {
		out = append(out, cloneObject(obj))
	}
	return out
}

func cloneObject(obj objectmodel.Record) objectmodel.Record {
	cloned := obj
	cloned.Checksums = append([]objectmodel.Checksum(nil), obj.Checksums...)
	cloned.NameAliases = append([]string(nil), obj.NameAliases...)
	if obj.AccessMethods != nil {
		methods := append([]objectmodel.AccessMethod(nil), (*obj.AccessMethods)...)
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

func mergeChecksums(group []objectmodel.Record) []objectmodel.Checksum {
	seen := make(map[string]struct{})
	merged := make([]objectmodel.Checksum, 0)
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

func mergeAccessMethods(group []objectmodel.Record) *[]objectmodel.AccessMethod {
	seen := make(map[string]struct{})
	methods := make([]objectmodel.AccessMethod, 0)
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
			method.AccessId = recordStringPtr(objectmodel.AccessMethodID(method.Type, url))
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

func mergeControlledAccess(group []objectmodel.Record) ([]string, bool) {
	resources := make([]string, 0)
	public := false
	for _, obj := range group {
		objectResources := objectmodel.AccessResources(&obj)
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

func mergeNameAliases(primary *string, group []objectmodel.Record) []string {
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
	return objectmodel.NormalizeNameAliases(primaryName, candidates)
}

func pickLatestNonZeroSize(group []objectmodel.Record, fallback int64) int64 {
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

func pickLatestStringPtr(group []objectmodel.Record, getter func(objectmodel.Record) *string, fallback *string) *string {
	best := fallback
	var bestTime time.Time
	bestID := ""
	for _, obj := range group {
		value := getter(obj)
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		when := canonicalObjectSortTime(obj)
		if best == nil || when.After(bestTime) || when.Equal(bestTime) && string(obj.Id) > bestID {
			trimmed := strings.TrimSpace(*value)
			best = &trimmed
			bestTime = when
			bestID = string(obj.Id)
		}
	}
	return best
}

func mergeStringPointerValues(getter func(objectmodel.Record) []string, group []objectmodel.Record) *[]string {
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

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}
