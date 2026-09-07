package storage

import (
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestOperationErrorExposesExactCodeAndBroadCategory(t *testing.T) {
	err := &OperationError{Kind: ErrorNotFound, Provider: "s3", Capability: "head"}
	if err.ErrorCode() != errorapi.ErrorCodeStorageNotFound {
		t.Fatalf("unexpected exact code: %s", err.ErrorCode())
	}
	if !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatal("storage not-found must match the broad not-found sentinel")
	}
}
