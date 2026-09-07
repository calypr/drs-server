package errorapi

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestCategoryForCodeCoversGeneratedCodes(t *testing.T) {
	codes := generatedErrorCodes(t)
	if len(codes) == 0 {
		t.Fatal("generated error code census is empty")
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			if category, ok := CategoryForCode(code); !ok {
				t.Fatalf("CategoryForCode(%q) is unmapped", code)
			} else if category == "" {
				t.Fatalf("CategoryForCode(%q) returned an empty category", code)
			}
		})
	}

	for code, wantCategory := range declaredSentinelCategories(t) {
		gotCategory, ok := CategoryForCode(code)
		if !ok {
			t.Fatalf("sentinel %q is not mapped by CategoryForCode", code)
		}
		if gotCategory != wantCategory {
			t.Fatalf("CategoryForCode(%q) = %q, want sentinel category %q", code, gotCategory, wantCategory)
		}
	}
}

func TestCategoryForCodeCoversFallbackCodes(t *testing.T) {
	for code, want := range map[ErrorCode]ErrorCategory{
		ErrorCodeInternalError: ErrorCategoryInternalError,
		ErrorCodeRequestFailed: ErrorCategoryInvalidInput,
	} {
		if got, ok := CategoryForCode(code); !ok || got != want {
			t.Fatalf("CategoryForCode(%q) = %q, %t; want %q, true", code, got, ok, want)
		}
	}
}

func TestDefinitionIdentityMatrix(t *testing.T) {
	codes := generatedErrorCodes(t)
	categories := make(map[ErrorCode]ErrorCategory, len(codes))
	for _, code := range codes {
		category, ok := CategoryForCode(code)
		if !ok {
			t.Fatalf("CategoryForCode(%q) is unmapped", code)
		}
		categories[code] = category
	}

	for _, sourceCode := range codes {
		for _, targetCode := range codes {
			source := Define(sourceCode, categories[sourceCode], "source")
			target := Define(targetCode, categories[targetCode], "target")
			want := sourceCode == targetCode || (IsBroadCode(targetCode) && categories[sourceCode] == categories[targetCode])
			if got := errors.Is(source, target); got != want {
				t.Fatalf("errors.Is(%q, %q) = %t, want %t", sourceCode, targetCode, got, want)
			}
		}
	}

	for _, code := range codes {
		category := categories[code]
		if got := errors.Is(Define(code, "intentionally-arbitrary", "source"), Define(code, category, "target")); !got {
			t.Fatalf("same code should match despite category mismatch for %q", code)
		}
	}
}

func TestDefinitionMatchingAndWrappedExtraction(t *testing.T) {
	exact := Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "missing object")
	if !errors.Is(exact, ErrNotFound) || !errors.Is(exact, ErrObjectNotFound) {
		t.Fatal("exact definition should match exact and broad sentinels")
	}
	if errors.Is(exact, ErrFileUsageNotFound) {
		t.Fatal("different exact definitions must not match")
	}
	if errors.Unwrap(exact) != nil {
		t.Fatal("category matching must not use causal unwrapping")
	}

	wrapped := fmt.Errorf("lookup failed: %w", exact)
	if code, ok := CodeOf(wrapped); !ok || code != ErrorCodeObjectNotFound {
		t.Fatalf("CodeOf(%v) = %q, %t", wrapped, code, ok)
	}
	if category, ok := CategoryOf(wrapped); !ok || category != ErrorCategoryNotFound {
		t.Fatalf("CategoryOf(%v) = %q, %t", wrapped, category, ok)
	}

	empty := emptyClassification{err: exact}
	if code, ok := CodeOf(empty); !ok || code != ErrorCodeObjectNotFound {
		t.Fatalf("CodeOf(empty outer wrapper) = %q, %t", code, ok)
	}
	joined := errors.Join(emptyClassification{}, exact)
	if category, ok := CategoryOf(joined); !ok || category != ErrorCategoryNotFound {
		t.Fatalf("CategoryOf(joined errors) = %q, %t", category, ok)
	}

	if code, ok := CodeOf(nil); ok || code != "" {
		t.Fatalf("CodeOf(nil) = %q, %t, want empty, false", code, ok)
	}
	if category, ok := CategoryOf(nil); ok || category != "" {
		t.Fatalf("CategoryOf(nil) = %q, %t, want empty, false", category, ok)
	}
	if code, ok := CodeOf(errors.New("uncoded")); ok || code != "" {
		t.Fatalf("CodeOf(uncoded) = %q, %t, want empty, false", code, ok)
	}
	if category, ok := CategoryOf(errors.New("uncategorized")); ok || category != "" {
		t.Fatalf("CategoryOf(uncategorized) = %q, %t, want empty, false", category, ok)
	}

	first := Define(ErrorCodeConflict, ErrorCategoryConflict, "first")
	second := Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "second")
	joined = errors.Join(fmt.Errorf("outer: %w", first), fmt.Errorf("outer: %w", second))
	if code, ok := CodeOf(joined); !ok || code != ErrorCodeConflict {
		t.Fatalf("CodeOf(joined) = %q, %t, want first code %q, true", code, ok, ErrorCodeConflict)
	}
	if category, ok := CategoryOf(joined); !ok || category != ErrorCategoryConflict {
		t.Fatalf("CategoryOf(joined) = %q, %t, want first category %q, true", category, ok, ErrorCategoryConflict)
	}
}

func TestDefinitionIsAsymmetry(t *testing.T) {
	tests := []struct {
		name   string
		source *Definition
		target *Definition
		want   bool
	}{
		{"same code mismatched category", Define(ErrorCodeObjectNotFound, ErrorCategoryConflict, "source"), Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "target"), true},
		{"different exact code same category", Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "source"), Define(ErrorCodeFileUsageNotFound, ErrorCategoryNotFound, "target"), false},
		{"broad target", Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "source"), ErrNotFound, true},
		{"exact target against broad source", ErrNotFound, ErrObjectNotFound, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := errors.Is(test.source, test.target); got != test.want {
				t.Fatalf("errors.Is(%v, %v) = %t, want %t", test.source, test.target, got, test.want)
			}
		})
	}
}

type emptyClassification struct{ err error }

func (e emptyClassification) Error() string                { return "empty classification" }
func (e emptyClassification) ErrorCode() ErrorCode         { return "" }
func (e emptyClassification) ErrorCategory() ErrorCategory { return "" }
func (e emptyClassification) Unwrap() error                { return e.err }

func TestCodeForStatusFallback(t *testing.T) {
	if got := CodeForStatus(http.StatusInternalServerError); got != ErrorCodeInternalError {
		t.Fatalf("CodeForStatus(500) = %q, want %q", got, ErrorCodeInternalError)
	}
	if got := CodeForStatus(599); got != ErrorCodeInternalError {
		t.Fatalf("CodeForStatus(599) = %q, want %q", got, ErrorCodeInternalError)
	}
	if got := CodeForStatus(http.StatusTeapot); got != ErrorCodeRequestFailed {
		t.Fatalf("CodeForStatus(418) = %q, want %q", got, ErrorCodeRequestFailed)
	}
	if got := CodeForStatus(0); got != ErrorCodeRequestFailed {
		t.Fatalf("CodeForStatus(0) = %q, want %q", got, ErrorCodeRequestFailed)
	}
	for _, status := range []int{0, http.StatusTeapot} {
		if got := CategoryForStatus(status); got != ErrorCategoryInvalidInput {
			t.Fatalf("CategoryForStatus(%d) = %q, want %q", status, got, ErrorCategoryInvalidInput)
		}
	}
	for _, status := range []int{http.StatusInternalServerError, 599} {
		if got := CategoryForStatus(status); got != ErrorCategoryInternalError {
			t.Fatalf("CategoryForStatus(%d) = %q, want %q", status, got, ErrorCategoryInternalError)
		}
	}
}

func FuzzClassificationExtraction(f *testing.F) {
	for _, seed := range []string{"", "missing object", strings.Repeat("x", 32)} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > 128 {
			payload = payload[:128]
		}
		depth := 0
		if len(payload) > 0 {
			depth = int(payload[0]) % 8
		}
		var err error = Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, payload)
		for i := 0; i < depth; i++ {
			err = fmt.Errorf("wrapped: %w", err)
		}
		joined := errors.Join(errors.New(payload), err)
		if code, ok := CodeOf(joined); !ok || code != ErrorCodeObjectNotFound {
			t.Fatalf("CodeOf(fuzzed wrapper) = %q, %t", code, ok)
		}
		if category, ok := CategoryOf(joined); !ok || category != ErrorCategoryNotFound {
			t.Fatalf("CategoryOf(fuzzed wrapper) = %q, %t", category, ok)
		}
	})
}

func generatedErrorCodes(t *testing.T) []ErrorCode {
	t.Helper()
	values := generatedConstants(t, "ErrorCode")
	codes := make([]ErrorCode, 0, len(values))
	for _, value := range values {
		codes = append(codes, ErrorCode(value))
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return codes
}

func declaredSentinelCategories(t *testing.T) map[ErrorCode]ErrorCategory {
	t.Helper()
	codeValues := generatedConstants(t, "ErrorCode")
	categoryValues := generatedConstants(t, "ErrorCategory")
	file := parseSource(t, "contract.go")
	definitions := make(map[ErrorCode]ErrorCategory)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, specification := range group.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok || len(values.Values) != len(values.Names) {
				continue
			}
			for index, expression := range values.Values {
				call, ok := expression.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					continue
				}
				function, ok := call.Fun.(*ast.Ident)
				if !ok || function.Name != "Define" {
					continue
				}
				codeName, ok := call.Args[0].(*ast.Ident)
				if !ok {
					continue
				}
				categoryName, ok := call.Args[1].(*ast.Ident)
				if !ok {
					continue
				}
				code, codeOK := codeValues[codeName.Name]
				category, categoryOK := categoryValues[categoryName.Name]
				if !codeOK || !categoryOK {
					t.Fatalf("could not resolve sentinel %q at index %d", values.Names[index].Name, index)
				}
				if _, duplicate := definitions[ErrorCode(code)]; duplicate {
					t.Fatalf("duplicate sentinel code %q", code)
				}
				definitions[ErrorCode(code)] = ErrorCategory(category)
			}
		}
	}
	if len(definitions) == 0 {
		t.Fatal("declared sentinel census is empty")
	}
	return definitions
}

func generatedConstants(t *testing.T, typeName string) map[string]string {
	t.Helper()
	file := parseSource(t, "error.gen.go")
	values := make(map[string]string)
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
			for index, name := range valueSpec.Names {
				valueIndex := index
				if valueIndex >= len(valueSpec.Values) {
					valueIndex = len(valueSpec.Values) - 1
				}
				literal, ok := valueSpec.Values[valueIndex].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("generated %s constant %q is not a string literal", typeName, name.Name)
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("unquote generated %s constant %q: %v", typeName, name.Name, err)
				}
				values[name.Name] = value
			}
		}
	}
	return values
}

func parseSource(t *testing.T, name string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return file
}
