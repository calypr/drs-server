package storage

import (
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestProjectStorageErrorExposesExactCodeAndBroadCategory(t *testing.T) {
	err := &Error{Kind: ErrorBucketUnavailable, Message: "bucket is not configured"}
	if err.ErrorCode() != errorapi.ErrorCodeStorageBucketUnavailable {
		t.Fatalf("unexpected exact code: %s", err.ErrorCode())
	}
	if !errors.Is(err, errorapi.ErrConflict) {
		t.Fatal("an unavailable configured bucket must match the broad conflict sentinel")
	}
}
