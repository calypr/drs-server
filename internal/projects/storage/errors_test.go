package storage

import (
	"errors"
	"testing"

	"github.com/calypr/syfon/internal/faults"
)

func TestProjectStorageErrorExposesExactCodeAndBroadCategory(t *testing.T) {
	err := &Error{Kind: ErrorBucketUnavailable, Message: "bucket is not configured"}
	if err.ErrorCode() != faults.CodeStorageBucketUnavailable {
		t.Fatalf("unexpected exact code: %s", err.ErrorCode())
	}
	if !errors.Is(err, faults.ErrConflict) {
		t.Fatal("an unavailable configured bucket must match the broad conflict sentinel")
	}
}
