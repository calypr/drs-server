package storage

import (
	"errors"
	"fmt"

	"github.com/calypr/syfon/apigen/errorapi"
)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorNotFound    ErrorKind = "not_found"
	ErrorForbidden   ErrorKind = "forbidden"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorIncomplete  ErrorKind = "incomplete"
	ErrorUnsupported ErrorKind = "unsupported"
	ErrorProvider    ErrorKind = "provider"
)

type OperationError struct {
	Kind       ErrorKind
	Provider   string
	Capability string
	Cause      error
}

func (e *OperationError) ErrorCode() errorapi.ErrorCode {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrorInvalid:
		return errorapi.ErrorCodeStorageInvalid
	case ErrorNotFound:
		return errorapi.ErrorCodeStorageNotFound
	case ErrorForbidden:
		return errorapi.ErrorCodeStorageForbidden
	case ErrorUnavailable:
		return errorapi.ErrorCodeStorageUnavailable
	case ErrorIncomplete:
		return errorapi.ErrorCodeStorageIncomplete
	case ErrorUnsupported:
		return errorapi.ErrorCodeStorageUnsupported
	case ErrorProvider:
		return errorapi.ErrorCodeStorageProviderError
	default:
		return errorapi.ErrorCodeStorageProviderError
	}
}

func (e *OperationError) ErrorCategory() errorapi.ErrorCategory {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrorNotFound:
		return errorapi.ErrorCategoryNotFound
	case ErrorForbidden:
		return errorapi.ErrorCategoryForbidden
	case ErrorInvalid, ErrorUnsupported:
		return errorapi.ErrorCategoryInvalidInput
	case ErrorUnavailable, ErrorIncomplete, ErrorProvider:
		return errorapi.ErrorCategoryUnavailable
	default:
		return errorapi.ErrorCategoryUnavailable
	}
}

func (e *OperationError) Is(target error) bool {
	definition := errorapi.Define(e.ErrorCode(), e.ErrorCategory(), e.Error())
	return errors.Is(definition, target)
}

func (e *OperationError) Error() string {
	if e == nil {
		return "storage operation error"
	}
	message := "storage operation failed"
	if e.Kind != "" {
		message = fmt.Sprintf("storage operation %s", e.Kind)
	}
	if e.Provider != "" {
		message += fmt.Sprintf(" for provider %s", e.Provider)
	}
	if e.Capability != "" {
		message += fmt.Sprintf(" (%s)", e.Capability)
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func operationError(kind ErrorKind, provider, capability string, cause error) error {
	return &OperationError{Kind: kind, Provider: provider, Capability: capability, Cause: cause}
}
