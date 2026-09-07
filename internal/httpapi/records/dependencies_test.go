package records

import (
	"context"

	"github.com/calypr/syfon/internal/objects"
)

type internalDRSTestFixture struct {
	ObjectService *objects.Service
}

type internalDRSObjectStore interface {
	objects.RecordReader
	objects.RecordWriter
	objects.AccessMethodWriter
	objects.AccessPolicyWriter
	objects.AliasStore
	objects.ContentReader
	objects.ChecksumScopeQuery
	objects.ScopeQuery
}

func newInternalDRSObjectManager(store internalDRSObjectStore) internalDRSTestFixture {
	deps := objects.Dependencies{
		Reader:        store,
		Writer:        store,
		AccessMethods: store,
		AccessPolicy:  store,
		Aliases:       store,
		Content:       store,
		ChecksumScope: store,
		Scope:         store,
	}
	if optional, ok := store.(objects.OptionalResourceQuery); ok {
		deps.Resources = optional
	}
	if optional, ok := store.(objects.OptionalPageQuery); ok {
		deps.Pages = optional
	}
	if optional, ok := store.(objects.OptionalURLQuery); ok {
		deps.URLPages = optional
	}
	if optional, ok := store.(objects.OptionalAuthorizedQuery); ok {
		deps.Authorized = optional
	}
	return internalDRSTestFixture{ObjectService: objects.NewService(deps)}
}

func (f internalDRSTestFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	return f.ObjectService.RegisterObjects(ctx, records)
}
