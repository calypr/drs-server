package records

import (
	"context"
)

func (m *mutationService) CreateObjectAlias(ctx context.Context, aliasID, canonicalID string) error {
	obj, err := m.recordReader.GetObject(ctx, canonicalID)
	if err != nil {
		return err
	}
	if err := requireObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return err
	}
	return m.aliases.CreateObjectAlias(ctx, aliasID, canonicalID)
}
