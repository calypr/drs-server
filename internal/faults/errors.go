package faults

import (
	"errors"

	"github.com/calypr/syfon/apigen/errorapi"
)

// Code is the exact machine-readable code written to the API envelope.
type Code = errorapi.ErrorCode

const (
	CodeNotFound      = errorapi.ErrorCodeNotFound
	CodeUnauthorized  = errorapi.ErrorCodeUnauthorized
	CodeForbidden     = errorapi.ErrorCodeForbidden
	CodeConflict      = errorapi.ErrorCodeConflict
	CodeInvalidInput  = errorapi.ErrorCodeInvalidInput
	CodeRateLimited   = errorapi.ErrorCodeRateLimited
	CodeUnavailable   = errorapi.ErrorCodeUnavailable
	CodeInternal      = errorapi.ErrorCodeInternalError
	CodeRequestFailed = errorapi.ErrorCodeRequestFailed

	CodeObjectNotFound            = errorapi.ErrorCodeObjectNotFound
	CodeBucketScopeNotFound       = errorapi.ErrorCodeBucketScopeNotFound
	CodeFileUsageNotFound         = errorapi.ErrorCodeFileUsageNotFound
	CodeMultipartUploadNotFound   = errorapi.ErrorCodeMultipartUploadNotFound
	CodeNoValidSHA256             = errorapi.ErrorCodeNoValidSha256
	CodeConflictingSHA256         = errorapi.ErrorCodeConflictingSha256
	CodeAccessMethodsRequired     = errorapi.ErrorCodeAccessMethodsRequired
	CodeObjectSizeImmutable       = errorapi.ErrorCodeObjectSizeImmutable
	CodeObjectChecksumImmutable   = errorapi.ErrorCodeObjectChecksumImmutable
	CodeBulkOverwriteConflict     = errorapi.ErrorCodeBulkOverwriteConflict
	CodeBucketNotConfigured       = errorapi.ErrorCodeBucketNotConfigured
	CodeObjectLocationUnavailable = errorapi.ErrorCodeObjectLocationUnavailable
	CodeAuthenticationRequired    = errorapi.ErrorCodeAuthenticationRequired
	CodeAccessDenied              = errorapi.ErrorCodeAccessDenied
	CodeStorageInvalid            = errorapi.ErrorCodeStorageInvalid
	CodeStorageNotFound           = errorapi.ErrorCodeStorageNotFound
	CodeStorageForbidden          = errorapi.ErrorCodeStorageForbidden
	CodeStorageUnavailable        = errorapi.ErrorCodeStorageUnavailable
	CodeStorageIncomplete         = errorapi.ErrorCodeStorageIncomplete
	CodeStorageUnsupported        = errorapi.ErrorCodeStorageUnsupported
	CodeStorageProviderError      = errorapi.ErrorCodeStorageProviderError
	CodeProjectScopeNotFound      = errorapi.ErrorCodeProjectScopeNotFound
	CodeStorageCredentialMissing  = errorapi.ErrorCodeStorageCredentialMissing
	CodeStorageBucketUnavailable  = errorapi.ErrorCodeStorageBucketUnavailable
	CodeStorageListingIncomplete  = errorapi.ErrorCodeStorageListingIncomplete
)

// Category is the broad error class used for portable control flow.
type Category = errorapi.ErrorCategory

const (
	CategoryNotFound     = errorapi.ErrorCategoryNotFound
	CategoryUnauthorized = errorapi.ErrorCategoryUnauthorized
	CategoryForbidden    = errorapi.ErrorCategoryForbidden
	CategoryConflict     = errorapi.ErrorCategoryConflict
	CategoryInvalidInput = errorapi.ErrorCategoryInvalidInput
	CategoryRateLimited  = errorapi.ErrorCategoryRateLimited
	CategoryUnavailable  = errorapi.ErrorCategoryUnavailable
	CategoryInternal     = errorapi.ErrorCategoryInternalError
)

// ErrorCategory is a descriptive alias for callers using the public API
// terminology.
type ErrorCategory = Category

const (
	ErrorCategoryNotFound     = CategoryNotFound
	ErrorCategoryUnauthorized = CategoryUnauthorized
	ErrorCategoryForbidden    = CategoryForbidden
	ErrorCategoryConflict     = CategoryConflict
	ErrorCategoryInvalidInput = CategoryInvalidInput
	ErrorCategoryRateLimited  = CategoryRateLimited
	ErrorCategoryUnavailable  = CategoryUnavailable
	ErrorCategoryInternal     = CategoryInternal
)

type Definition struct {
	code     Code
	category Category
	message  string
}

func (e *Definition) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *Definition) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	switch target := target.(type) {
	case *Definition:
		return e.code == target.code || (isBroadCode(target.code) && e.category == target.category)
	default:
		return false
	}
}

func (e *Definition) ErrorCode() Code {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *Definition) ErrorCategory() Category {
	if e == nil {
		return ""
	}
	return e.category
}

// Define creates a source-defined error code with a broad category.
func Define(code Code, category Category, message string) *Definition {
	return &Definition{code: code, category: category, message: message}
}

func New(code Code, category Category, message string) *Definition {
	return Define(code, category, message)
}

var (
	ErrNotFound               = Define(CodeNotFound, CategoryNotFound, "not found")
	ErrUnauthorized           = Define(CodeUnauthorized, CategoryUnauthorized, "unauthorized")
	ErrForbidden              = Define(CodeForbidden, CategoryForbidden, "forbidden")
	ErrConflict               = Define(CodeConflict, CategoryConflict, "conflict")
	ErrInvalidInput           = Define(CodeInvalidInput, CategoryInvalidInput, "invalid input")
	ErrRateLimited            = Define(CodeRateLimited, CategoryRateLimited, "rate limited")
	ErrUnavailable            = Define(CodeUnavailable, CategoryUnavailable, "unavailable")
	ErrObjectNotFound         = Define(CodeObjectNotFound, CategoryNotFound, "object not found")
	ErrBucketScopeNotFound    = Define(CodeBucketScopeNotFound, CategoryNotFound, "bucket scope not found")
	ErrFileUsageNotFound      = Define(CodeFileUsageNotFound, CategoryNotFound, "file usage not found")
	ErrAuthenticationRequired = Define(CodeAuthenticationRequired, CategoryUnauthorized, "authentication required")
	ErrAccessDenied           = Define(CodeAccessDenied, CategoryForbidden, "access denied")
)

func isBroadCode(code Code) bool {
	switch code {
	case CodeNotFound, CodeUnauthorized, CodeForbidden, CodeConflict, CodeInvalidInput, CodeRateLimited, CodeUnavailable, CodeInternal, CodeRequestFailed:
		return true
	default:
		return false
	}
}

func CodeOf(err error) (Code, bool) {
	var coded interface{ ErrorCode() Code }
	if !errors.As(err, &coded) {
		return "", false
	}
	code := coded.ErrorCode()
	return code, code != ""
}

func CategoryOf(err error) (Category, bool) {
	var coded interface{ ErrorCategory() Category }
	if !errors.As(err, &coded) {
		return "", false
	}
	category := coded.ErrorCategory()
	return category, category != ""
}

func CategoryForCode(code Code) (Category, bool) {
	switch code {
	case CodeNotFound, CodeObjectNotFound, CodeBucketScopeNotFound, CodeFileUsageNotFound, CodeMultipartUploadNotFound, CodeObjectLocationUnavailable, CodeStorageNotFound, CodeProjectScopeNotFound, CodeStorageCredentialMissing:
		return CategoryNotFound, true
	case CodeUnauthorized, CodeAuthenticationRequired:
		return CategoryUnauthorized, true
	case CodeForbidden, CodeAccessDenied, CodeStorageForbidden:
		return CategoryForbidden, true
	case CodeConflict, CodeConflictingSHA256, CodeObjectSizeImmutable, CodeObjectChecksumImmutable, CodeBulkOverwriteConflict, CodeStorageBucketUnavailable:
		return CategoryConflict, true
	case CodeInvalidInput, CodeRequestFailed, CodeNoValidSHA256, CodeAccessMethodsRequired, CodeStorageInvalid, CodeStorageUnsupported:
		return CategoryInvalidInput, true
	case CodeRateLimited:
		return CategoryRateLimited, true
	case CodeUnavailable, CodeBucketNotConfigured, CodeStorageUnavailable, CodeStorageIncomplete, CodeStorageProviderError, CodeStorageListingIncomplete:
		return CategoryUnavailable, true
	case CodeInternal:
		return CategoryInternal, true
	default:
		return "", false
	}
}

func IsNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}
