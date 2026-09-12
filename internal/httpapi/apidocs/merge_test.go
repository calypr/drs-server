package apidocs

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergedOpenAPIServersPreserveRouteBases(t *testing.T) {
	document := mergedOpenAPITestDocument(t)
	if got := mergedServerURL(t, document["servers"]); got != "https://{serverURL}/ga4gh/drs/v1" {
		t.Fatalf("global server = %q, want DRS server", got)
	}
	paths := documentMap(t, document, "paths")
	for _, path := range []string{"/info/lfs/objects/batch", "/data/buckets", "/index/v1/metrics/files", "/index"} {
		pathItem := mapAt(t, paths, path)
		if got := mergedServerURL(t, pathItem["servers"]); got != "/" {
			t.Fatalf("path %q server = %q, want source root", path, got)
		}
	}
	if _, exists := mapAt(t, paths, "/objects/{object_id}")["servers"]; exists {
		t.Fatal("DRS path unexpectedly has an imported server override")
	}
}

func TestMergedOpenAPIServerPrecedence(t *testing.T) {
	dst := map[string]interface{}{
		"paths": map[string]interface{}{
			"/existing": map[string]interface{}{
				"servers": []interface{}{map[string]interface{}{"url": "path-winner"}},
				"get": map[string]interface{}{
					"servers": []interface{}{map[string]interface{}{"url": "operation-winner"}},
				},
			},
		},
	}
	src := map[string]interface{}{
		"servers": []interface{}{map[string]interface{}{"url": "source-default"}},
		"paths": map[string]interface{}{
			"/existing": map[string]interface{}{
				"get": map[string]interface{}{
					"servers": []interface{}{map[string]interface{}{"url": "source-operation"}},
				},
			},
			"/new": map[string]interface{}{
				"get": map[string]interface{}{
					"servers": []interface{}{map[string]interface{}{"url": "new-operation"}},
				},
			},
		},
	}

	mergeImportedSpec(dst, src, "fixture")
	paths := dst["paths"].(map[string]interface{})
	existing := paths["/existing"].(map[string]interface{})
	if got := mergedServerURL(t, existing["servers"]); got != "path-winner" {
		t.Fatalf("existing path server = %q, want path-winner", got)
	}
	if got := mergedServerURL(t, existing["get"].(map[string]interface{})["servers"]); got != "operation-winner" {
		t.Fatalf("existing operation server = %q, want operation-winner", got)
	}
	newPath := paths["/new"].(map[string]interface{})
	if got := mergedServerURL(t, newPath["servers"]); got != "source-default" {
		t.Fatalf("source default server = %q, want source-default", got)
	}
	if got := mergedServerURL(t, newPath["get"].(map[string]interface{})["servers"]); got != "new-operation" {
		t.Fatalf("new operation server = %q, want new-operation", got)
	}
}

func TestMergedOpenAPIComponentsKeepDRSAndLFSIdentity(t *testing.T) {
	document := mergedOpenAPITestDocument(t)
	assertMergedLocalReferencesResolve(t, document)

	paths := documentMap(t, document, "paths")
	lfsMetadata := mapAt(t, paths, "/info/lfs/objects/metadata")["post"].(map[string]interface{})
	lfsContent := lfsMetadata["requestBody"].(map[string]interface{})["content"].(map[string]interface{})
	lfsSchema := lfsContent["application/vnd.git-lfs+json"].(map[string]interface{})["schema"].(map[string]interface{})
	lfsCandidateRef := lfsSchema["$ref"].(string)
	if lfsCandidateRef != "#/components/schemas/MetadataSubmitRequest" {
		t.Fatalf("LFS metadata request ref = %q, want original imported identifier", lfsCandidateRef)
	}
	lfsMetadataSchema := mergedComponent(t, document, lfsCandidateRef)
	lfsCandidateSchema := lfsMetadataSchema["properties"].(map[string]interface{})["candidates"].(map[string]interface{})["items"].(map[string]interface{})
	lfsCandidate := mergedComponent(t, document, lfsCandidateSchema["$ref"].(string))
	if required, ok := lfsCandidate["required"]; ok && containsString(required, "size") {
		t.Fatalf("LFS candidate required fields = %v, must not require size", required)
	}
	lfsAccessRef := lfsCandidate["properties"].(map[string]interface{})["access_methods"].(map[string]interface{})["items"].(map[string]interface{})["$ref"].(string)
	lfsAccess := mergedComponent(t, document, lfsAccessRef)
	if _, hasEnum := lfsAccess["properties"].(map[string]interface{})["type"].(map[string]interface{})["enum"]; hasEnum {
		t.Fatal("LFS access method inherited the DRS type enum")
	}

	register := mapAt(t, paths, "/objects/register")["post"].(map[string]interface{})
	registerRef := register["requestBody"].(map[string]interface{})["$ref"].(string)
	registerBody := mergedComponent(t, document, registerRef)
	registerSchema := registerBody["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	drsCandidateRef := registerSchema["properties"].(map[string]interface{})["candidates"].(map[string]interface{})["items"].(map[string]interface{})["$ref"].(string)
	if drsCandidateRef != "#/components/schemas/DrsObjectCandidate" {
		t.Fatalf("DRS candidate ref = %q, want canonical DRS component", drsCandidateRef)
	}
	drsCandidate := mergedComponent(t, document, drsCandidateRef)
	for _, required := range []string{"size", "checksums"} {
		if !containsString(drsCandidate["required"], required) {
			t.Fatalf("DRS candidate required fields = %v, missing %q", drsCandidate["required"], required)
		}
	}
	drsAccessRef := drsCandidate["properties"].(map[string]interface{})["access_methods"].(map[string]interface{})["items"].(map[string]interface{})["$ref"].(string)
	if drsAccessRef != "#/components/schemas/AccessMethod" {
		t.Fatalf("DRS access method ref = %q, want canonical DRS component", drsAccessRef)
	}
	drsAccess := mergedComponent(t, document, drsAccessRef)
	if _, ok := drsAccess["properties"].(map[string]interface{})["type"].(map[string]interface{})["enum"]; !ok {
		t.Fatal("canonical DRS access method lost its type enum")
	}
	if findReferenceWithSiblingAllOf(document) {
		t.Fatal("merged document contains a reference with a sibling allOf")
	}
}

func mergedOpenAPITestDocument(t *testing.T) map[string]interface{} {
	t.Helper()
	raw, err := buildMergedOpenAPISpec()
	if err != nil {
		t.Fatalf("build merged spec: %v", err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode merged spec: %v", err)
	}
	return document
}

func documentMap(t *testing.T, document map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	value, ok := document[key].(map[string]interface{})
	if !ok {
		t.Fatalf("document %q = %#v, want map", key, document[key])
	}
	return value
}

func mapAt(t *testing.T, values map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	value, ok := values[key].(map[string]interface{})
	if !ok {
		t.Fatalf("map %q = %#v, want map", key, values[key])
	}
	return value
}

func mergedServerURL(t *testing.T, value interface{}) string {
	t.Helper()
	servers, ok := value.([]interface{})
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %#v, want one server", value)
	}
	server, ok := servers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("server = %#v, want map", servers[0])
	}
	url, ok := server["url"].(string)
	if !ok {
		t.Fatalf("server url = %#v, want string", server["url"])
	}
	return url
}

func mergedComponent(t *testing.T, document map[string]interface{}, ref string) map[string]interface{} {
	t.Helper()
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) {
		t.Fatalf("reference %q is not local", ref)
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	if len(parts) != 2 {
		t.Fatalf("reference %q does not name a component", ref)
	}
	components := documentMap(t, document, "components")
	section := mapAt(t, components, parts[0])
	return mapAt(t, section, parts[1])
}

func assertMergedLocalReferencesResolve(t *testing.T, document map[string]interface{}) {
	t.Helper()
	var walk func(interface{})
	walk = func(value interface{}) {
		switch current := value.(type) {
		case map[string]interface{}:
			if ref, ok := current["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/") {
				mergedComponent(t, document, ref)
			}
			for _, nested := range current {
				walk(nested)
			}
		case []interface{}:
			for _, nested := range current {
				walk(nested)
			}
		}
	}
	walk(document)
}

func findReferenceWithSiblingAllOf(value interface{}) bool {
	switch current := value.(type) {
	case map[string]interface{}:
		_, hasRef := current["$ref"]
		_, hasAllOf := current["allOf"]
		if hasRef && hasAllOf {
			return true
		}
		for _, nested := range current {
			if findReferenceWithSiblingAllOf(nested) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range current {
			if findReferenceWithSiblingAllOf(nested) {
				return true
			}
		}
	}
	return false
}

func containsString(value interface{}, want string) bool {
	items, ok := value.([]interface{})
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
