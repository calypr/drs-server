package faults

import (
	"errors"
	"testing"
)

func TestNotFoundClassifier(t *testing.T) {
	wrapped := errors.New("outer: " + ErrNotFound.Error())
	if IsNotFoundError(wrapped) {
		t.Fatalf("expected direct string wrapping not to satisfy errors.Is")
	}
	if !IsNotFoundError(ErrNotFound) {
		t.Fatalf("expected ErrNotFound to classify")
	}
}
