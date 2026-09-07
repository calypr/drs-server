package errorapi

import (
	"errors"
	"net/http"
)

type Definition struct {
	code     ErrorCode
	category ErrorCategory
	message  string
}

func Define(code ErrorCode, category ErrorCategory, message string) *Definition {
	return &Definition{code: code, category: category, message: message}
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
	targetDefinition, ok := target.(*Definition)
	if !ok || targetDefinition == nil {
		return false
	}
	return e.code == targetDefinition.code || (IsBroadCode(targetDefinition.code) && e.category == targetDefinition.category)
}

func (e *Definition) ErrorCode() ErrorCode {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *Definition) ErrorCategory() ErrorCategory {
	if e == nil {
		return ""
	}
	return e.category
}

var (
	ErrNotFound                  = Define(ErrorCodeNotFound, ErrorCategoryNotFound, "not found")
	ErrUnauthorized              = Define(ErrorCodeUnauthorized, ErrorCategoryUnauthorized, "unauthorized")
	ErrForbidden                 = Define(ErrorCodeForbidden, ErrorCategoryForbidden, "forbidden")
	ErrConflict                  = Define(ErrorCodeConflict, ErrorCategoryConflict, "conflict")
	ErrInvalidInput              = Define(ErrorCodeInvalidInput, ErrorCategoryInvalidInput, "invalid input")
	ErrRateLimited               = Define(ErrorCodeRateLimited, ErrorCategoryRateLimited, "rate limited")
	ErrUnavailable               = Define(ErrorCodeUnavailable, ErrorCategoryUnavailable, "unavailable")
	ErrObjectNotFound            = Define(ErrorCodeObjectNotFound, ErrorCategoryNotFound, "object not found")
	ErrBucketScopeNotFound       = Define(ErrorCodeBucketScopeNotFound, ErrorCategoryNotFound, "bucket scope not found")
	ErrFileUsageNotFound         = Define(ErrorCodeFileUsageNotFound, ErrorCategoryNotFound, "file usage not found")
	ErrMultipartUploadNotFound   = Define(ErrorCodeMultipartUploadNotFound, ErrorCategoryNotFound, "multipart upload not found")
	ErrNoValidSHA256             = Define(ErrorCodeNoValidSha256, ErrorCategoryInvalidInput, "no valid sha256 values provided")
	ErrConflictingSHA256         = Define(ErrorCodeConflictingSha256, ErrorCategoryConflict, "conflicting sha256 values provided")
	ErrAccessMethodsRequired     = Define(ErrorCodeAccessMethodsRequired, ErrorCategoryInvalidInput, "candidate must include at least one access method with a non-empty url")
	ErrObjectSizeImmutable       = Define(ErrorCodeObjectSizeImmutable, ErrorCategoryConflict, "object size is immutable")
	ErrObjectChecksumImmutable   = Define(ErrorCodeObjectChecksumImmutable, ErrorCategoryConflict, "object checksum identity is immutable")
	ErrBulkOverwriteConflict     = Define(ErrorCodeBulkOverwriteConflict, ErrorCategoryConflict, "bulk overwrite conflict")
	ErrBucketNotConfigured       = Define(ErrorCodeBucketNotConfigured, ErrorCategoryUnavailable, "no bucket configured")
	ErrObjectLocationUnavailable = Define(ErrorCodeObjectLocationUnavailable, ErrorCategoryNotFound, "no object location available")
	ErrAuthenticationRequired    = Define(ErrorCodeAuthenticationRequired, ErrorCategoryUnauthorized, "authentication required")
	ErrAccessDenied              = Define(ErrorCodeAccessDenied, ErrorCategoryForbidden, "access denied")
	ErrStorageInvalid            = Define(ErrorCodeStorageInvalid, ErrorCategoryInvalidInput, "storage invalid")
	ErrStorageNotFound           = Define(ErrorCodeStorageNotFound, ErrorCategoryNotFound, "storage not found")
	ErrStorageForbidden          = Define(ErrorCodeStorageForbidden, ErrorCategoryForbidden, "storage forbidden")
	ErrStorageUnavailable        = Define(ErrorCodeStorageUnavailable, ErrorCategoryUnavailable, "storage unavailable")
	ErrStorageIncomplete         = Define(ErrorCodeStorageIncomplete, ErrorCategoryUnavailable, "storage incomplete")
	ErrStorageUnsupported        = Define(ErrorCodeStorageUnsupported, ErrorCategoryInvalidInput, "storage unsupported")
	ErrStorageProviderError      = Define(ErrorCodeStorageProviderError, ErrorCategoryUnavailable, "storage provider error")
	ErrProjectScopeNotFound      = Define(ErrorCodeProjectScopeNotFound, ErrorCategoryNotFound, "project scope not found")
	ErrStorageCredentialMissing  = Define(ErrorCodeStorageCredentialMissing, ErrorCategoryNotFound, "storage credential missing")
	ErrStorageBucketUnavailable  = Define(ErrorCodeStorageBucketUnavailable, ErrorCategoryConflict, "storage bucket unavailable")
	ErrStorageListingIncomplete  = Define(ErrorCodeStorageListingIncomplete, ErrorCategoryUnavailable, "storage listing incomplete")
)

func IsBroadCode(code ErrorCode) bool {
	switch code {
	case ErrorCodeNotFound, ErrorCodeUnauthorized, ErrorCodeForbidden, ErrorCodeConflict, ErrorCodeInvalidInput, ErrorCodeRateLimited, ErrorCodeUnavailable, ErrorCodeInternalError, ErrorCodeRequestFailed:
		return true
	default:
		return false
	}
}

func CodeOf(err error) (ErrorCode, bool) {
	var code ErrorCode
	visitErrors(err, func(candidate error) bool {
		coded, ok := candidate.(interface{ ErrorCode() ErrorCode })
		if !ok || coded.ErrorCode() == "" {
			return false
		}
		code = coded.ErrorCode()
		return true
	})
	return code, code != ""
}

func CategoryOf(err error) (ErrorCategory, bool) {
	var category ErrorCategory
	visitErrors(err, func(candidate error) bool {
		categorized, ok := candidate.(interface{ ErrorCategory() ErrorCategory })
		if !ok || categorized.ErrorCategory() == "" {
			return false
		}
		category = categorized.ErrorCategory()
		return true
	})
	return category, category != ""
}

func visitErrors(err error, visit func(error) bool) bool {
	if err == nil || visit(err) {
		return err != nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if visitErrors(child, visit) {
				return true
			}
		}
		return false
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return visitErrors(wrapped.Unwrap(), visit)
	}
	return false
}

func CategoryForCode(code ErrorCode) (ErrorCategory, bool) {
	switch code {
	case ErrorCodeNotFound, ErrorCodeObjectNotFound, ErrorCodeBucketScopeNotFound, ErrorCodeFileUsageNotFound, ErrorCodeMultipartUploadNotFound, ErrorCodeObjectLocationUnavailable, ErrorCodeStorageNotFound, ErrorCodeProjectScopeNotFound, ErrorCodeStorageCredentialMissing:
		return ErrorCategoryNotFound, true
	case ErrorCodeUnauthorized, ErrorCodeAuthenticationRequired:
		return ErrorCategoryUnauthorized, true
	case ErrorCodeForbidden, ErrorCodeAccessDenied, ErrorCodeStorageForbidden:
		return ErrorCategoryForbidden, true
	case ErrorCodeConflict, ErrorCodeConflictingSha256, ErrorCodeObjectSizeImmutable, ErrorCodeObjectChecksumImmutable, ErrorCodeBulkOverwriteConflict, ErrorCodeStorageBucketUnavailable:
		return ErrorCategoryConflict, true
	case ErrorCodeInvalidInput, ErrorCodeRequestFailed, ErrorCodeNoValidSha256, ErrorCodeAccessMethodsRequired, ErrorCodeStorageInvalid, ErrorCodeStorageUnsupported:
		return ErrorCategoryInvalidInput, true
	case ErrorCodeRateLimited:
		return ErrorCategoryRateLimited, true
	case ErrorCodeUnavailable, ErrorCodeBucketNotConfigured, ErrorCodeStorageUnavailable, ErrorCodeStorageIncomplete, ErrorCodeStorageProviderError, ErrorCodeStorageListingIncomplete:
		return ErrorCategoryUnavailable, true
	case ErrorCodeInternalError:
		return ErrorCategoryInternalError, true
	default:
		return "", false
	}
}

func IsNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func CodeForStatus(status int) ErrorCode {
	switch status {
	case http.StatusNotFound:
		return ErrorCodeNotFound
	case http.StatusUnauthorized:
		return ErrorCodeUnauthorized
	case http.StatusForbidden:
		return ErrorCodeForbidden
	case http.StatusConflict:
		return ErrorCodeConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return ErrorCodeInvalidInput
	case http.StatusTooManyRequests:
		return ErrorCodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrorCodeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return ErrorCodeInternalError
		}
		return ErrorCodeRequestFailed
	}
}

func CategoryForStatus(status int) ErrorCategory {
	switch status {
	case http.StatusNotFound:
		return ErrorCategoryNotFound
	case http.StatusUnauthorized:
		return ErrorCategoryUnauthorized
	case http.StatusForbidden:
		return ErrorCategoryForbidden
	case http.StatusConflict:
		return ErrorCategoryConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return ErrorCategoryInvalidInput
	case http.StatusTooManyRequests:
		return ErrorCategoryRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrorCategoryUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return ErrorCategoryInternalError
		}
		category, _ := CategoryForCode(CodeForStatus(status))
		return category
	}
}
