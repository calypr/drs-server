package storage

import (
	"errors"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
)

// InspectionMode controls the amount of inventory returned by InspectProjectStorage.
type InspectionMode string

const (
	ModeItems   InspectionMode = "items"
	ModeExists  InspectionMode = "exists"
	ModeSummary InspectionMode = "summary"
)

type InventoryOptions struct {
	IncludeHead bool
	ExactPrefix bool
	MaxKeys     int32
}

type InspectionOptions struct {
	Mode        InspectionMode
	IncludeHead bool
	PathPrefix  string
}

type probeStatus string

const (
	probePresent     probeStatus = "present"
	probeNotFound    probeStatus = "not_found"
	probeForbidden   probeStatus = "forbidden"
	probeInvalid     probeStatus = "invalid"
	probeUnsupported probeStatus = "unsupported"
	probeError       probeStatus = "error"
)

type validationStatus string

const (
	validationNotRequested validationStatus = "not_requested"
	validationMatched      validationStatus = "matched"
	validationMismatched   validationStatus = "mismatched"
	validationUnverifiable validationStatus = "unverifiable"
)

type InspectRequest struct {
	ID                string
	Organization      string
	Project           string
	Key               string
	Scheme            string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedSHA256    string
	ExpectedName      string
}

type objectMetadata struct {
	ObjectURL   string
	Provider    string
	Bucket      string
	Key         string
	Path        string
	SizeBytes   int64
	MetaSHA256  string
	ETag        string
	LastModTime time.Time
}

type ErrorKind string

const (
	ErrorInvalidInput      ErrorKind = "invalid_input"
	ErrorScopeNotFound     ErrorKind = "scope_not_found"
	ErrorCredentialMissing ErrorKind = "credential_missing"
	ErrorPermissionDenied  ErrorKind = "permission_denied"
	ErrorObjectNotFound    ErrorKind = "object_not_found"
	ErrorBucketUnavailable ErrorKind = "bucket_unavailable"
	ErrorListingIncomplete ErrorKind = "listing_incomplete"
	ErrorUnsupported       ErrorKind = "unsupported"
)

// Error is a typed maintenance error. HTTP adapters map its Kind to status
// codes; domain callers can use errors.As without importing HTTP packages.
type Error struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "project storage operation failed"
	}
	message := e.PublicMessage()
	if e.Cause != nil {
		return message + ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) PublicMessage() string {
	if e == nil {
		return "project storage operation failed"
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) ErrorCode() errorapi.ErrorCode {
	if e == nil {
		return errorapi.ErrorCodeInternalError
	}
	switch e.Kind {
	case ErrorInvalidInput:
		return errorapi.ErrorCodeInvalidInput
	case ErrorScopeNotFound:
		return errorapi.ErrorCodeProjectScopeNotFound
	case ErrorCredentialMissing:
		return errorapi.ErrorCodeStorageCredentialMissing
	case ErrorPermissionDenied:
		return errorapi.ErrorCodeAccessDenied
	case ErrorObjectNotFound:
		return errorapi.ErrorCodeObjectNotFound
	case ErrorBucketUnavailable:
		return errorapi.ErrorCodeStorageBucketUnavailable
	case ErrorListingIncomplete:
		return errorapi.ErrorCodeStorageListingIncomplete
	case ErrorUnsupported:
		return errorapi.ErrorCodeStorageUnsupported
	default:
		return errorapi.ErrorCodeInternalError
	}
}

func (e *Error) ErrorCategory() errorapi.ErrorCategory {
	category, ok := errorapi.CategoryForCode(e.ErrorCode())
	if !ok {
		return errorapi.ErrorCategoryInternalError
	}
	return category
}

func (e *Error) Is(target error) bool {
	definition := errorapi.Define(e.ErrorCode(), e.ErrorCategory(), e.Error())
	return errors.Is(definition, target)
}
