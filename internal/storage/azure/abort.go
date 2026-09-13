package azure

import (
	"context"

	"github.com/calypr/syfon/internal/storage"
)

// Azure block blobs do not expose a safe operation that removes only the
// uncommitted blocks for one upload without risking a committed destination.
// The provider lifecycle policy owns that cleanup; the durable Syfon session
// can therefore be retired safely with this successful no-op.
func (b *backend) AbortMultipart(context.Context, storage.ProviderBinding, storage.AbortMultipartRequest) error {
	return nil
}
