package records

import (
	"context"

	"github.com/calypr/syfon/apigen/errorapi"
	objectmodel "github.com/calypr/syfon/internal/objects"
)

func (s *Service) RegisterObjects(ctx context.Context, objs []objectmodel.Record) error {
	if err := s.validateExistingContentRead(ctx, objs); err != nil {
		return err
	}
	if err := bulkObjectMethodError(ctx, objs, objectMethodCreate); err != nil {
		return err
	}
	return s.store.RegisterObjects(ctx, objs)
}

func (s *Service) validateExistingContentRead(ctx context.Context, objs []objectmodel.Record) error {
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
		existing, err := s.store.GetObjectsByChecksum(ctx, sha)
		if err != nil {
			return err
		}
		for j := range existing {
			if existing[j].PublicRead || hasObjectMethod(ctx, &existing[j], objectMethodRead) {
				continue
			}
			return errorapi.ErrAccessDenied
		}
	}
	return nil
}
