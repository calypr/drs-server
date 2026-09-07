package storage

import "fmt"

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
