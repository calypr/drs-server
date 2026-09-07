package storage

import (
	"context"

	"github.com/calypr/syfon/internal/buckets"
)

// CredentialLookup reads one configured bucket credential by identifier or
// physical bucket name.
type CredentialLookup interface {
	GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error)
}
