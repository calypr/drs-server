package records

import (
	"context"

	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
)

type internalDRSTestFixture struct {
	ObjectService *objectrecords.Service
}

func newInternalDRSObjectManager(store objectrecords.ObjectStore) internalDRSTestFixture {
	return internalDRSTestFixture{ObjectService: objectrecords.NewService(store)}
}

func (f internalDRSTestFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	return f.ObjectService.RegisterObjects(ctx, records)
}
