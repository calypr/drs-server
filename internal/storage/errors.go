package storage

import (
	"errors"
	"fmt"

	"github.com/calypr/syfon/internal/faults"
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

func (e *OperationError) ErrorCode() faults.Code {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrorInvalid:
		return faults.CodeStorageInvalid
	case ErrorNotFound:
		return faults.CodeStorageNotFound
	case ErrorForbidden:
		return faults.CodeStorageForbidden
	case ErrorUnavailable:
		return faults.CodeStorageUnavailable
	case ErrorIncomplete:
		return faults.CodeStorageIncomplete
	case ErrorUnsupported:
		return faults.CodeStorageUnsupported
	case ErrorProvider:
		return faults.CodeStorageProviderError
	default:
		return faults.CodeStorageProviderError
	}
}

func (e *OperationError) ErrorCategory() faults.Category {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrorNotFound:
		return faults.CategoryNotFound
	case ErrorForbidden:
		return faults.CategoryForbidden
	case ErrorInvalid, ErrorUnsupported:
		return faults.CategoryInvalidInput
	case ErrorUnavailable, ErrorIncomplete, ErrorProvider:
		return faults.CategoryUnavailable
	default:
		return faults.CategoryUnavailable
	}
}

func (e *OperationError) Is(target error) bool {
	definition := faults.Define(e.ErrorCode(), e.ErrorCategory(), e.Error())
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
