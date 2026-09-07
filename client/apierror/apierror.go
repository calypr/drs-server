package apierror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
)

var (
	ErrNotFound                  = errors.New("resource not found")
	ErrUnauthorized              = errors.New("unauthorized")
	ErrForbidden                 = errors.New("forbidden")
	ErrConflict                  = errors.New("conflict")
	ErrInvalidInput              = errors.New("invalid input")
	ErrRateLimited               = errors.New("rate limited")
	ErrUnavailable               = errors.New("service unavailable")
	ErrObjectNotFound            = &codeError{code: CodeObjectNotFound}
	ErrBucketScopeNotFound       = &codeError{code: CodeBucketScopeNotFound}
	ErrFileUsageNotFound         = &codeError{code: CodeFileUsageNotFound}
	ErrMultipartUploadNotFound   = &codeError{code: CodeMultipartUploadNotFound}
	ErrNoValidSHA256             = &codeError{code: CodeNoValidSHA256}
	ErrConflictingSHA256         = &codeError{code: CodeConflictingSHA256}
	ErrAccessMethodsRequired     = &codeError{code: CodeAccessMethodsRequired}
	ErrObjectSizeImmutable       = &codeError{code: CodeObjectSizeImmutable}
	ErrObjectChecksumImmutable   = &codeError{code: CodeObjectChecksumImmutable}
	ErrBulkOverwriteConflict     = &codeError{code: CodeBulkOverwriteConflict}
	ErrBucketNotConfigured       = &codeError{code: CodeBucketNotConfigured}
	ErrObjectLocationUnavailable = &codeError{code: CodeObjectLocationUnavailable}
	ErrAuthenticationRequired    = &codeError{code: CodeAuthenticationRequired}
	ErrAccessDenied              = &codeError{code: CodeAccessDenied}
	ErrStorageInvalid            = &codeError{code: CodeStorageInvalid}
	ErrStorageNotFound           = &codeError{code: CodeStorageNotFound}
	ErrStorageForbidden          = &codeError{code: CodeStorageForbidden}
	ErrStorageUnavailable        = &codeError{code: CodeStorageUnavailable}
	ErrStorageIncomplete         = &codeError{code: CodeStorageIncomplete}
	ErrStorageUnsupported        = &codeError{code: CodeStorageUnsupported}
	ErrStorageProviderError      = &codeError{code: CodeStorageProviderError}
	ErrProjectScopeNotFound      = &codeError{code: CodeProjectScopeNotFound}
	ErrStorageCredentialMissing  = &codeError{code: CodeStorageCredentialMissing}
	ErrStorageBucketUnavailable  = &codeError{code: CodeStorageBucketUnavailable}
	ErrStorageListingIncomplete  = &codeError{code: CodeStorageListingIncomplete}
)

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

type ErrorCode = Code
type ErrorCategory = Category

type codeError struct{ code Code }

func (e *codeError) Error() string   { return string(e.code) }
func (e *codeError) ErrorCode() Code { return e.code }

type APIError struct {
	Code      Code
	Category  Category
	Status    int
	Message   string
	RequestID string
	Method    string
	URL       string
	Headers   http.Header
	Body      string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Status < http.StatusInternalServerError {
		message = strings.TrimSpace(e.Body)
	}
	if message == "" {
		message = http.StatusText(e.Status)
		if message == "" && e.Status >= http.StatusInternalServerError {
			message = http.StatusText(http.StatusInternalServerError)
		}
	}
	if message == "" {
		message = "request failed"
	}
	return fmt.Sprintf("%s %s: status %d body=%s", e.Method, e.URL, e.Status, message)
}

func (e *APIError) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	switch target := target.(type) {
	case *codeError:
		return e.Code == target.code
	}
	if e.Code != "" {
		if code, ok := target.(interface{ ErrorCode() Code }); ok && e.Code == code.ErrorCode() {
			return true
		}
	}
	if e.Category != "" {
		if category, ok := target.(interface{ ErrorCategory() Category }); ok && e.Category == category.ErrorCategory() {
			return true
		}
	}
	switch target {
	case ErrNotFound:
		return e.Category == CategoryNotFound
	case ErrUnauthorized:
		return e.Category == CategoryUnauthorized
	case ErrForbidden:
		return e.Category == CategoryForbidden
	case ErrConflict:
		return e.Category == CategoryConflict
	case ErrInvalidInput:
		return e.Category == CategoryInvalidInput
	case ErrRateLimited:
		return e.Category == CategoryRateLimited
	case ErrUnavailable:
		return e.Category == CategoryUnavailable
	default:
		return false
	}
}

func FromResponse(resp *http.Response, body []byte) *APIError {
	err := &APIError{
		Status: httpStatus(resp),
		Body:   strings.TrimSpace(string(body)),
	}
	if resp != nil {
		err.Headers = resp.Header.Clone()
		if resp.Request != nil {
			err.Method = resp.Request.Method
			if resp.Request.URL != nil {
				err.URL = resp.Request.URL.String()
			}
		}
		err.RequestID = firstNonEmpty(resp.Header.Get("X-Request-ID"), resp.Header.Get("X-Request-Id"))
	}
	decodePayload(err, body)
	if err.Code == "" {
		err.Code = codeForStatus(err.Status)
	}
	if err.Category == "" {
		if category, ok := categoryForCode(err.Code); ok {
			err.Category = category
		} else {
			err.Category = categoryForStatus(err.Status)
		}
	}
	if err.Message == "" {
		err.Message = err.Body
	}
	if err.Status >= http.StatusInternalServerError {
		err.Message = publicStatusText(err.Status)
	}
	return err
}

func decodePayload(err *APIError, body []byte) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return
	}
	decodeObject(err, payload)
}

func decodeObject(err *APIError, payload map[string]any) {
	if payload == nil {
		return
	}
	err.Code = Code(firstNonEmpty(string(err.Code), valueString(payload["code"]), valueString(payload["error_code"]), valueString(payload["type"])))
	err.Category = Category(firstNonEmpty(string(err.Category), valueString(payload["category"]), valueString(payload["error_category"])))
	err.Message = firstNonEmpty(err.Message, valueString(payload["message"]), valueString(payload["msg"]))
	err.RequestID = firstNonEmpty(err.RequestID, valueString(payload["request_id"]), valueString(payload["requestId"]))
	if nested, ok := payload["error"].(map[string]any); ok {
		decodeObject(err, nested)
	} else if nested := valueString(payload["error"]); nested != "" {
		err.Message = firstNonEmpty(err.Message, nested)
	}
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func codeForStatus(status int) Code {
	switch status {
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusConflict:
		return CodeConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return CodeInvalidInput
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return CodeInternal
		}
		return CodeRequestFailed
	}
}

func categoryForCode(code Code) (Category, bool) {
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

func categoryForStatus(status int) Category {
	switch status {
	case http.StatusNotFound:
		return CategoryNotFound
	case http.StatusUnauthorized:
		return CategoryUnauthorized
	case http.StatusForbidden:
		return CategoryForbidden
	case http.StatusConflict:
		return CategoryConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return CategoryInvalidInput
	case http.StatusTooManyRequests:
		return CategoryRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CategoryUnavailable
	case http.StatusInternalServerError:
		return CategoryInternal
	default:
		if status >= http.StatusInternalServerError {
			return CategoryInternal
		}
		return CategoryInternal
	}
}

func publicStatusText(status int) string {
	if message := http.StatusText(status); message != "" {
		return message
	}
	return http.StatusText(http.StatusInternalServerError)
}

func httpStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
