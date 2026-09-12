package apidocs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	openapispec "github.com/calypr/syfon/apigen/openapi"
	"gopkg.in/yaml.v3"
)

func findNamedOpenAPISpecPath(fileName string) (string, bool) {
	candidates := []string{
		filepath.Join("apigen", "openapi", fileName),
		filepath.Join(filepath.Dir(os.Args[0]), "apigen", "openapi", fileName),
	}

	if _, thisFile, _, ok := runtime.Caller(0); ok {
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
		candidates = append(candidates, filepath.Join(repoRoot, "apigen", "openapi", fileName))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func buildMergedOpenAPISpec() ([]byte, error) {
	drsSpec, err := loadSpecYAMLByName("openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("DRS spec missing: %w", err)
	}
	lfsSpec, err := loadSpecYAMLByName("lfs.openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("LFS spec missing: %w", err)
	}
	merged := drsSpec
	mergeImportedSpec(merged, lfsSpec, "lfs")
	if bucketSpec, err := loadSpecYAMLByName("bucket.openapi.yaml"); err == nil {
		mergeImportedSpec(merged, bucketSpec, "bucket")
	}
	for _, extra := range []string{
		"metrics.openapi.yaml",
		"internal.openapi.yaml",
	} {
		if s, err := loadSpecYAMLByName(extra); err == nil {
			mergeImportedSpec(merged, s, componentNamespace(extra))
		}
	}
	if compatSpec, err := loadSpecYAMLByName("compat.openapi.yaml"); err == nil {
		mergeImportedSpec(merged, compatSpec, "compat")
	}

	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func loadSpecYAMLByName(fileName string) (map[string]interface{}, error) {
	raw, err := loadSpecBytesByName(fileName)
	if err != nil {
		return nil, err
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func loadSpecBytesByName(fileName string) ([]byte, error) {
	raw, err := openapispec.ReadSpec(fileName)
	if err == nil {
		return raw, nil
	}
	path, ok := findNamedOpenAPISpecPath(fileName)
	if !ok {
		return nil, err
	}
	return os.ReadFile(path)
}

type componentKey struct {
	kind string
	name string
}

var componentSections = []string{
	"schemas",
	"responses",
	"parameters",
	"examples",
	"requestBodies",
	"headers",
	"securitySchemes",
	"links",
	"callbacks",
}

func mergeImportedSpec(dst, src map[string]interface{}, namespace string) {
	imported := cloneYAMLMap(src)
	renamed := qualifyImportedComponents(dst, imported, namespace)
	rewriteYAMLReferences(imported, renamed)
	mergeImportedPaths(dst, imported)
	mergeImportedComponents(dst, imported)
}

func componentNamespace(fileName string) string {
	base := strings.TrimSuffix(filepath.Base(fileName), ".openapi.yaml")
	if base == "" {
		return "component"
	}
	var normalized strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			normalized.WriteRune(r)
		default:
			normalized.WriteByte('_')
		}
	}
	if normalized.Len() == 0 {
		return "component"
	}
	return normalized.String()
}

func qualifyImportedComponents(dst, src map[string]interface{}, namespace string) map[componentKey]string {
	renamed := make(map[componentKey]string)
	srcComponents, ok := asYAMLMap(src["components"])
	if !ok {
		return renamed
	}
	dstComponents, _ := asYAMLMap(dst["components"])
	qualifiedComponents := cloneYAMLMap(srcComponents)
	for _, section := range componentSections {
		srcSection, ok := asYAMLMap(srcComponents[section])
		if !ok {
			continue
		}
		sourceNames := make([]string, 0, len(srcSection))
		for name := range srcSection {
			sourceNames = append(sourceNames, name)
		}
		sort.Strings(sourceNames)

		// Reserve destination names and imported names that can remain unchanged
		// before selecting names for collisions. This keeps generated names from
		// shadowing another component in the same section.
		usedNames := make(map[string]struct{}, len(srcSection))
		if dstSection, ok := asYAMLMap(dstComponents[section]); ok {
			for name := range dstSection {
				usedNames[name] = struct{}{}
			}
		}
		for _, name := range sourceNames {
			if !componentNameInUse(dstComponents, section, name) {
				usedNames[name] = struct{}{}
			}
		}

		qualifiedSection := make(map[string]interface{}, len(srcSection))
		for _, name := range sourceNames {
			qualifiedName := name
			if componentNameInUse(dstComponents, section, name) {
				baseName := namespace + "_" + name
				qualifiedName = baseName
				for suffix := 2; ; suffix++ {
					if _, exists := usedNames[qualifiedName]; !exists {
						break
					}
					qualifiedName = fmt.Sprintf("%s_%d", baseName, suffix)
				}
				renamed[componentKey{kind: section, name: name}] = qualifiedName
			}
			qualifiedSection[qualifiedName] = srcSection[name]
			usedNames[qualifiedName] = struct{}{}
		}
		qualifiedComponents[section] = qualifiedSection
	}
	src["components"] = qualifiedComponents
	return renamed
}

func componentNameInUse(components map[string]interface{}, section, name string) bool {
	if components == nil {
		return false
	}
	sectionMap, ok := asYAMLMap(components[section])
	if !ok {
		return false
	}
	_, ok = sectionMap[name]
	return ok
}

func mergeImportedPaths(dst, src map[string]interface{}) {
	srcPaths, ok := asYAMLMap(src["paths"])
	if !ok {
		return
	}
	dstPaths, ok := asYAMLMap(dst["paths"])
	if !ok {
		dstPaths = make(map[string]interface{})
		dst["paths"] = dstPaths
	}
	sourceServers, hasSourceServers := src["servers"]
	for pathName, rawPath := range srcPaths {
		srcPath, pathIsMap := asYAMLMap(rawPath)
		if !pathIsMap {
			if _, exists := dstPaths[pathName]; !exists {
				dstPaths[pathName] = rawPath
			}
			continue
		}
		srcPath = cloneYAMLMap(srcPath)
		if _, hasPathServers := srcPath["servers"]; !hasPathServers && hasSourceServers {
			srcPath["servers"] = cloneYAMLValue(sourceServers)
		}

		dstPathRaw, exists := dstPaths[pathName]
		if !exists {
			dstPaths[pathName] = srcPath
			continue
		}
		dstPath, pathIsMap := asYAMLMap(dstPathRaw)
		if !pathIsMap {
			dstPaths[pathName] = srcPath
			continue
		}
		mergePathItem(dstPath, srcPath)
		dstPaths[pathName] = dstPath
	}
}

func mergePathItem(dst, src map[string]interface{}) {
	mergeMapPreservingServers(dst, src)
}

func mergeMapPreservingServers(dst, src map[string]interface{}) {
	for key, sourceValue := range src {
		if key == "servers" {
			if _, exists := dst[key]; !exists {
				dst[key] = sourceValue
			}
			continue
		}
		existingValue, exists := dst[key]
		if !exists {
			dst[key] = sourceValue
			continue
		}
		existingMap, existingIsMap := asYAMLMap(existingValue)
		sourceMap, sourceIsMap := asYAMLMap(sourceValue)
		if existingIsMap && sourceIsMap {
			mergeMapPreservingServers(existingMap, sourceMap)
			dst[key] = existingMap
			continue
		}
		dst[key] = sourceValue
	}
}

func mergeImportedComponents(dst, src map[string]interface{}) {
	srcComponents, ok := asYAMLMap(src["components"])
	if !ok {
		return
	}
	dstComponents, ok := asYAMLMap(dst["components"])
	if !ok {
		dstComponents = make(map[string]interface{})
		dst["components"] = dstComponents
	}
	for section, rawSection := range srcComponents {
		srcSection, ok := asYAMLMap(rawSection)
		if !ok {
			if _, exists := dstComponents[section]; !exists {
				dstComponents[section] = rawSection
			}
			continue
		}
		dstSection, ok := asYAMLMap(dstComponents[section])
		if !ok {
			dstSection = make(map[string]interface{})
			dstComponents[section] = dstSection
		}
		for name, value := range srcSection {
			if _, exists := dstSection[name]; !exists {
				dstSection[name] = value
			}
		}
	}
}

func rewriteYAMLReferences(value interface{}, renamed map[componentKey]string) {
	switch current := value.(type) {
	case map[string]interface{}:
		for key, nested := range current {
			if text, ok := nested.(string); ok {
				current[key] = rewriteLocalComponentReference(text, renamed)
				continue
			}
			rewriteYAMLReferences(nested, renamed)
		}
	case []interface{}:
		for index, nested := range current {
			if text, ok := nested.(string); ok {
				current[index] = rewriteLocalComponentReference(text, renamed)
				continue
			}
			rewriteYAMLReferences(nested, renamed)
		}
	}
}

func rewriteLocalComponentReference(ref string, renamed map[componentKey]string) string {
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) {
		return ref
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	if len(parts) < 2 {
		return ref
	}
	kind := decodeJSONPointerSegment(parts[0])
	name := decodeJSONPointerSegment(parts[1])
	qualifiedName, ok := renamed[componentKey{kind: kind, name: name}]
	if !ok {
		return ref
	}
	parts[1] = encodeJSONPointerSegment(qualifiedName)
	return prefix + strings.Join(parts, "/")
}

func decodeJSONPointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~1", "/"), "~0", "~")
}

func encodeJSONPointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func asYAMLMap(value interface{}) (map[string]interface{}, bool) {
	result, ok := value.(map[string]interface{})
	return result, ok
}

func cloneYAMLMap(value map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(value))
	for key, nested := range value {
		cloned[key] = cloneYAMLValue(nested)
	}
	return cloned
}

func cloneYAMLValue(value interface{}) interface{} {
	switch current := value.(type) {
	case map[string]interface{}:
		return cloneYAMLMap(current)
	case []interface{}:
		cloned := make([]interface{}, len(current))
		for index, nested := range current {
			cloned[index] = cloneYAMLValue(nested)
		}
		return cloned
	default:
		return value
	}
}
