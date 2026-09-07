package records

import (
	"context"

	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
)

type internalDRSTestFixture struct {
	ObjectService *objectrecords.Service
}

type internalDRSObjectStore interface {
	objectrecords.RecordReader
	objectrecords.RecordWriter
	objectrecords.AccessMethodWriter
	objectrecords.AccessPolicyWriter
	objectrecords.AliasStore
	objectrecords.ContentReader
	objectrecords.ChecksumScopeQuery
	objectrecords.ScopeQuery
}

func newInternalDRSObjectManager(store internalDRSObjectStore) internalDRSTestFixture {
	deps := objectrecords.Dependencies{
		Reader:        store,
		Writer:        store,
		AccessMethods: store,
		AccessPolicy:  store,
		Aliases:       store,
		Content:       store,
		ChecksumScope: store,
		Scope:         store,
	}
	if optional, ok := store.(objectrecords.OptionalResourceQuery); ok {
		deps.Resources = optional
	}
	if optional, ok := store.(objectrecords.OptionalPageQuery); ok {
		deps.Pages = optional
	}
	if optional, ok := store.(objectrecords.OptionalURLQuery); ok {
		deps.URLPages = optional
	}
	if optional, ok := store.(objectrecords.OptionalAuthorizedQuery); ok {
		deps.Authorized = optional
	}
	return internalDRSTestFixture{ObjectService: objectrecords.NewService(deps)}
}

func (f internalDRSTestFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	return f.ObjectService.RegisterObjects(ctx, records)
}
