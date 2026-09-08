package records

import (
	"context"
)

func (s *Service) CreateObjectAlias(ctx context.Context, aliasID, canonicalID string) error {
	obj, err := s.store.GetObject(ctx, canonicalID)
	if err != nil {
		return err
	}
	if err := requireObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return err
	}
	return s.store.CreateObjectAlias(ctx, aliasID, canonicalID)
}
