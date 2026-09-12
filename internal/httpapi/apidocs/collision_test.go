package apidocs

import "testing"

func TestMergeImportedComponentsQualifiesOnlySameSectionCollisions(t *testing.T) {
	dst := map[string]interface{}{
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Collision": map[string]interface{}{"description": "DRS"},
			},
		},
	}
	src := map[string]interface{}{
		"paths": map[string]interface{}{
			"/fixture": map[string]interface{}{
				"get": map[string]interface{}{
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"$ref": "#/components/responses/Collision",
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Collision": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"unique": map[string]interface{}{
							"$ref": "#/components/schemas/Unique",
						},
					},
				},
				"Unique": map[string]interface{}{"type": "string"},
			},
			"responses": map[string]interface{}{
				"Collision": map[string]interface{}{
					"description": "imported response",
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{
							"schema": map[string]interface{}{
								"$ref": "#/components/schemas/Collision",
							},
						},
					},
				},
			},
		},
	}

	mergeImportedSpec(dst, src, "fixture")

	components := dst["components"].(map[string]interface{})
	schemas := components["schemas"].(map[string]interface{})
	if _, ok := schemas["Unique"]; !ok {
		t.Fatal("unique imported schema lost its original identifier")
	}
	qualifiedCollision, ok := schemas["fixture_Collision"].(map[string]interface{})
	if !ok {
		t.Fatal("colliding imported schema was not qualified")
	}
	if got := qualifiedCollision["properties"].(map[string]interface{})["unique"].(map[string]interface{})["$ref"]; got != "#/components/schemas/Unique" {
		t.Fatalf("unique schema reference = %q, want original identifier", got)
	}

	responses := components["responses"].(map[string]interface{})
	importedResponse, ok := responses["Collision"].(map[string]interface{})
	if !ok {
		t.Fatal("non-colliding response was not kept in its original section identifier")
	}
	responseSchema := importedResponse["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if got := responseSchema["$ref"]; got != "#/components/schemas/fixture_Collision" {
		t.Fatalf("response schema reference = %q, want qualified collision", got)
	}

	pathResponse := dst["paths"].(map[string]interface{})["/fixture"].(map[string]interface{})["get"].(map[string]interface{})["responses"].(map[string]interface{})["200"].(map[string]interface{})
	if got := pathResponse["$ref"]; got != "#/components/responses/Collision" {
		t.Fatalf("response reference = %q, want original response identifier", got)
	}
}
