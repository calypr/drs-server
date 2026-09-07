package apidocs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
	mergeSpecSection(merged, lfsSpec, "paths")
	mergeSpecSection(merged, lfsSpec, "components")
	if bucketSpec, err := loadSpecYAMLByName("bucket.openapi.yaml"); err == nil {
		mergeSpecSection(merged, bucketSpec, "paths")
		mergeSpecSection(merged, bucketSpec, "components")
	}
	for _, extra := range []string{
		"metrics.openapi.yaml",
		"internal.openapi.yaml",
	} {
		if s, err := loadSpecYAMLByName(extra); err == nil {
			mergeSpecSection(merged, s, "paths")
			mergeSpecSection(merged, s, "components")
		}
	}
	if compatSpec, err := loadSpecYAMLByName("compat.openapi.yaml"); err == nil {
		mergeSpecSection(merged, compatSpec, "paths")
		mergeSpecSection(merged, compatSpec, "components")
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

func mergeSpecSection(dst map[string]interface{}, src map[string]interface{}, section string) {
	srcVal, ok := src[section]
	if !ok {
		return
	}
	srcMap, ok := srcVal.(map[string]interface{})
	if !ok {
		return
	}

	dstVal, ok := dst[section]
	if !ok {
		dst[section] = srcMap
		return
	}
	dstMap, ok := dstVal.(map[string]interface{})
	if !ok {
		dst[section] = srcMap
		return
	}
	deepMerge(dstMap, srcMap)
}

func deepMerge(dst map[string]interface{}, src map[string]interface{}) {
	for k, v := range src {
		if existing, ok := dst[k]; ok {
			existingMap, existingOK := existing.(map[string]interface{})
			vMap, vOK := v.(map[string]interface{})
			if existingOK && vOK {
				deepMerge(existingMap, vMap)
				dst[k] = existingMap
				continue
			}
		}
		dst[k] = v
	}
}
