package access

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/faults"
)

func TestAuthorizationError_UnwrapAndClassifiers(t *testing.T) {
	err := &AuthorizationError{Method: "read", RecordID: "obj-1"}
	if !errors.Is(err, faults.ErrUnauthorized) {
		t.Fatalf("expected AuthorizationError to unwrap to ErrUnauthorized")
	}
}

func TestAuthorizationError_PublicMessage(t *testing.T) {
	err := &AuthorizationError{
		Method:             "delete",
		RecordID:           "did-1",
		Resources:          []string{"/organization/a/project/p1"},
		DeniedRecords:      2,
		TotalRecords:       3,
		TruncatedResources: 1,
	}
	msg := err.PublicMessage()
	if !strings.Contains(msg, "missing delete access") || !strings.Contains(msg, "first denied record") || !strings.Contains(msg, "a/p1") {
		t.Fatalf("unexpected public message: %q", msg)
	}
}
