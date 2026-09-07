package storage

import (
	"errors"
	"testing"

	"github.com/calypr/syfon/internal/faults"
)

func TestOperationErrorExposesExactCodeAndBroadCategory(t *testing.T) {
	err := &OperationError{Kind: ErrorNotFound, Provider: "s3", Capability: "head"}
	if err.ErrorCode() != faults.CodeStorageNotFound {
		t.Fatalf("unexpected exact code: %s", err.ErrorCode())
	}
	if !errors.Is(err, faults.ErrNotFound) {
		t.Fatal("storage not-found must match the broad not-found sentinel")
	}
}
