package errorapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestCategoryForCodeCoversGeneratedCodes(t *testing.T) {
	tests := []struct {
		code     ErrorCode
		category ErrorCategory
	}{
		{ErrorCodeNotFound, ErrorCategoryNotFound},
		{ErrorCodeUnauthorized, ErrorCategoryUnauthorized},
		{ErrorCodeForbidden, ErrorCategoryForbidden},
		{ErrorCodeConflict, ErrorCategoryConflict},
		{ErrorCodeInvalidInput, ErrorCategoryInvalidInput},
		{ErrorCodeRateLimited, ErrorCategoryRateLimited},
		{ErrorCodeUnavailable, ErrorCategoryUnavailable},
		{ErrorCodeInternalError, ErrorCategoryInternalError},
		{ErrorCodeRequestFailed, ErrorCategoryInvalidInput},
		{ErrorCodeObjectNotFound, ErrorCategoryNotFound},
		{ErrorCodeBucketScopeNotFound, ErrorCategoryNotFound},
		{ErrorCodeFileUsageNotFound, ErrorCategoryNotFound},
		{ErrorCodeMultipartUploadNotFound, ErrorCategoryNotFound},
		{ErrorCodeNoValidSha256, ErrorCategoryInvalidInput},
		{ErrorCodeConflictingSha256, ErrorCategoryConflict},
		{ErrorCodeAccessMethodsRequired, ErrorCategoryInvalidInput},
		{ErrorCodeObjectSizeImmutable, ErrorCategoryConflict},
		{ErrorCodeObjectChecksumImmutable, ErrorCategoryConflict},
		{ErrorCodeBulkOverwriteConflict, ErrorCategoryConflict},
		{ErrorCodeBucketNotConfigured, ErrorCategoryUnavailable},
		{ErrorCodeObjectLocationUnavailable, ErrorCategoryNotFound},
		{ErrorCodeAuthenticationRequired, ErrorCategoryUnauthorized},
		{ErrorCodeAccessDenied, ErrorCategoryForbidden},
		{ErrorCodeStorageInvalid, ErrorCategoryInvalidInput},
		{ErrorCodeStorageNotFound, ErrorCategoryNotFound},
		{ErrorCodeStorageForbidden, ErrorCategoryForbidden},
		{ErrorCodeStorageUnavailable, ErrorCategoryUnavailable},
		{ErrorCodeStorageIncomplete, ErrorCategoryUnavailable},
		{ErrorCodeStorageUnsupported, ErrorCategoryInvalidInput},
		{ErrorCodeStorageProviderError, ErrorCategoryUnavailable},
		{ErrorCodeProjectScopeNotFound, ErrorCategoryNotFound},
		{ErrorCodeStorageCredentialMissing, ErrorCategoryNotFound},
		{ErrorCodeStorageBucketUnavailable, ErrorCategoryConflict},
		{ErrorCodeStorageListingIncomplete, ErrorCategoryUnavailable},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			category, ok := CategoryForCode(test.code)
			if !ok || category != test.category {
				t.Fatalf("CategoryForCode(%q) = %q, %t; want %q, true", test.code, category, ok, test.category)
			}
		})
	}
	if len(tests) != 34 {
		t.Fatalf("test table covers %d generated codes, want 34", len(tests))
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
