package openapi

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestErrorSchemaEnumsMatchGeneratedConstants(t *testing.T) {
	raw, err := ReadSpec("error.openapi.yaml")
	if err != nil {
		t.Fatalf("read error schema: %v", err)
	}

	for _, typeName := range []string{"ErrorCode", "ErrorCategory"} {
		t.Run(typeName, func(t *testing.T) {
			schemaValues, err := schemaEnumValues(raw, typeName)
			if err != nil {
				t.Fatal(err)
			}
			generatedValues := generatedEnumValues(t, typeName)
			assertSameValues(t, schemaValues, generatedValues)
		})
	}
}

func schemaEnumValues(raw []byte, typeName string) ([]string, error) {
	sectionIndent := -1
	inEnum := false
	values := make([]string, 0)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if sectionIndent < 0 {
			if trimmed == typeName+":" {
				sectionIndent = indent
			}
			continue
		}
		if trimmed != "" && indent <= sectionIndent {
			break
		}
		if trimmed == "enum:" {
			inEnum = true
			continue
		}
		if !inEnum {
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			if trimmed != "" {
				break
			}
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if value == "" {
			return nil, fmt.Errorf("error schema %s: empty enum value", typeName)
		}
		if quoted, err := strconv.Unquote(value); err == nil {
			value = quoted
		}
		values = append(values, value)
	}
	if sectionIndent < 0 {
		return nil, fmt.Errorf("error schema %s: schema section not found", typeName)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("error schema %s: enum is empty", typeName)
	}
	return values, nil
}

func generatedEnumValues(t *testing.T, typeName string) []string {
	t.Helper()
	path := filepath.Join("..", "errorapi", "error.gen.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse generated error constants: %v", err)
	}
	values := make([]string, 0)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		declaredType := ""
		for _, specification := range group.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if valueSpec.Type != nil {
				identifier, ok := valueSpec.Type.(*ast.Ident)
				if !ok {
					declaredType = ""
					continue
				}
				declaredType = identifier.Name
			}
			if declaredType != typeName || len(valueSpec.Values) == 0 {
				continue
			}
			for index := range valueSpec.Names {
				valueIndex := index
				if valueIndex >= len(valueSpec.Values) {
					valueIndex = len(valueSpec.Values) - 1
				}
				literal, ok := valueSpec.Values[valueIndex].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("generated %s constant %q is not a string literal", typeName, valueSpec.Names[index].Name)
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("unquote generated %s constant %q: %v", typeName, valueSpec.Names[index].Name, err)
				}
				values = append(values, value)
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("generated %s census is empty", typeName)
	}
	return values
}

func assertSameValues(t *testing.T, want, got []string) {
	t.Helper()
	want = append([]string(nil), want...)
	got = append([]string(nil), got...)
	sort.Strings(want)
	sort.Strings(got)
	if len(want) != len(got) {
		t.Fatalf("enum values have different lengths: schema=%d generated=%d\nschema=%v\ngenerated=%v", len(want), len(got), want, got)
	}
	for index := range want {
		if want[index] != got[index] {
			t.Fatalf("enum values differ at index %d: schema=%q generated=%q\nschema=%v\ngenerated=%v", index, want[index], got[index], want, got)
		}
	}
}
