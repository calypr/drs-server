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

func TestDefinitionMatchesItsExactCodeAndBroadCategory(t *testing.T) {
	exact := Define(CodeObjectNotFound, CategoryNotFound, "missing object")
	if !errors.Is(exact, ErrNotFound) {
		t.Fatal("exact definition should match its broad category sentinel")
	}
	if !errors.Is(exact, exact) {
		t.Fatal("exact definition should match itself")
	}
	other := Define(CodeFileUsageNotFound, CategoryNotFound, "missing usage")
	if errors.Is(exact, other) {
		t.Fatal("different exact definitions must not match")
	}
	if errors.Unwrap(exact) != nil {
		t.Fatal("category must be represented by Is, not causal unwrapping")
	}
}
