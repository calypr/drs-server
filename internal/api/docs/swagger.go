package docs

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	openapispec "github.com/calypr/syfon/apigen/openapi"
	"github.com/calypr/syfon/internal/api/routeutil"
	"github.com/calypr/syfon/internal/config"
	"github.com/gofiber/fiber/v3"
	"gopkg.in/yaml.v3"
)

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DRS Server API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: "` + config.RouteOpenAPISpec + `",
        dom_id: "#swagger-ui"
      });
    };
  </script>
</body>
</html>
`

// RegisterSwaggerRoutes adds Swagger/OpenAPI docs endpoints.
func RegisterSwaggerRoutes(router fiber.Router) {
	router.Get(routeutil.FiberPath(config.RouteSwaggerUI), routeutil.Handler(handleSwaggerUI))
	router.Get(routeutil.FiberPath(config.RouteSwaggerUIAlt), routeutil.Handler(handleSwaggerUI))
	// OpenAPI is intentionally exposed only under /index for proxy compatibility.
	router.Get(routeutil.FiberPath(config.RouteOpenAPISpec), routeutil.Handler(handleOpenAPISpec))
	router.Get(routeutil.FiberPath(config.RouteLFSSpec), routeutil.Handler(handleLFSOpenAPISpec))
	router.Get(routeutil.FiberPath(config.RouteBucketSpec), routeutil.Handler(handleBucketOpenAPISpec))
	router.Get(routeutil.FiberPath(config.RouteInternalSpec), routeutil.Handler(handleInternalOpenAPISpec))
}

func handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(swaggerUIHTML)); err != nil {
		log.Printf("write swagger ui response: %v", err)
	}
}

func handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	merged, err := buildMergedOpenAPISpec()
	if err != nil {
		http.Error(w, "OpenAPI spec file not found: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(merged); err != nil {
		log.Printf("write merged openapi spec response: %v", err)
	}
}

func handleLFSOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	specBytes, err := loadSpecBytesByName("lfs.openapi.yaml")
	if err != nil {
		http.Error(w, "LFS OpenAPI spec file not found: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(specBytes); err != nil {
		log.Printf("write lfs openapi spec response: %v", err)
	}
}

func handleBucketOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	specBytes, err := loadSpecBytesByName("bucket.openapi.yaml")
	if err != nil {
		http.Error(w, "Bucket OpenAPI spec file not found: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(specBytes); err != nil {
		log.Printf("write bucket openapi spec response: %v", err)
	}
}

func handleInternalOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	specBytes, err := loadSpecBytesByName("internal.openapi.yaml")
	if err != nil {
		http.Error(w, "Internal OpenAPI spec file not found: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(specBytes); err != nil {
		log.Printf("write internal openapi spec response: %v", err)
	}
}

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
	// Compatibility spec is optional; merge it if present.
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
	// Prefer embedded specs so runtime path/layout does not matter in deployments.
	raw, err := openapispec.ReadSpec(fileName)
	if err == nil {
		return raw, nil
	}
	// Fallback for local dev scenarios where embeddings might be stale.
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
