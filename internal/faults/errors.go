package faults

import "errors"

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrInvalidInput = errors.New("invalid input")
)

func IsNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}
