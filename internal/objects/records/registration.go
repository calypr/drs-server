package records

import (
	"context"

	"github.com/calypr/syfon/internal/faults"
	objectmodel "github.com/calypr/syfon/internal/objects"
)

func (m *mutationService) RegisterObjects(ctx context.Context, objs []objectmodel.Record) error {
	if err := m.validateExistingContentRead(ctx, objs); err != nil {
		return err
	}
	if err := bulkObjectMethodError(ctx, objs, objectMethodCreate); err != nil {
		return err
	}
	return m.recordWriter.RegisterObjects(ctx, objs)
}

func (m *mutationService) validateExistingContentRead(ctx context.Context, objs []objectmodel.Record) error {
	seen := make(map[string]struct{})
	for i := range objs {
		sha, ok := objectmodel.CanonicalSHA256(objs[i].Checksums)
		if !ok || sha == "" {
			continue
		}
		if _, done := seen[sha]; done {
			continue
		}
		seen[sha] = struct{}{}
		existing, err := m.content.GetObjectsByChecksum(ctx, sha)
		if err != nil {
			return err
		}
		for j := range existing {
			if existing[j].PublicRead || hasObjectMethod(ctx, &existing[j], objectMethodRead) {
				continue
			}
			return faults.ErrUnauthorized
		}
	}
	return nil
}
