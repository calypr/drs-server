package faults

import "errors"

type Code string

const (
	CodeNotFound      Code = "not_found"
	CodeUnauthorized  Code = "unauthorized"
	CodeForbidden     Code = "forbidden"
	CodeConflict      Code = "conflict"
	CodeInvalidInput  Code = "invalid_input"
	CodeRateLimited   Code = "rate_limited"
	CodeUnavailable   Code = "unavailable"
	CodeInternal      Code = "internal_error"
	CodeRequestFailed Code = "request_failed"
)

type codedError struct {
	code    Code
	message string
}

func (e *codedError) Error() string {
	return e.message
}

func (e *codedError) ErrorCode() Code {
	return e.code
}

var (
	ErrNotFound     = &codedError{code: CodeNotFound, message: "not found"}
	ErrUnauthorized = &codedError{code: CodeUnauthorized, message: "unauthorized"}
	ErrForbidden    = &codedError{code: CodeForbidden, message: "forbidden"}
	ErrConflict     = &codedError{code: CodeConflict, message: "conflict"}
	ErrInvalidInput = &codedError{code: CodeInvalidInput, message: "invalid input"}
	ErrRateLimited  = &codedError{code: CodeRateLimited, message: "rate limited"}
	ErrUnavailable  = &codedError{code: CodeUnavailable, message: "unavailable"}
)

func CodeOf(err error) (Code, bool) {
	var coded interface {
		ErrorCode() Code
	}
	if !errors.As(err, &coded) {
		return "", false
	}
	code := coded.ErrorCode()
	return code, code != ""
}

func IsNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}
