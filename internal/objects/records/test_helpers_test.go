package records_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

func newTestService(backend any, _ ...any) *objectrecords.Service {
	deps := objectrecords.Dependencies{
		Reader:        backend.(objectrecords.RecordReader),
		Writer:        backend.(objectrecords.RecordWriter),
		AccessMethods: backend.(objectrecords.AccessMethodWriter),
		AccessPolicy:  backend.(objectrecords.AccessPolicyWriter),
		Aliases:       backend.(objectrecords.AliasStore),
		Content:       backend.(objectrecords.ContentReader),
		ChecksumScope: backend.(objectrecords.ChecksumScopeQuery),
		Scope:         backend.(objectrecords.ScopeQuery),
	}
	if optional, ok := backend.(objectrecords.OptionalResourceQuery); ok {
		deps.Resources = optional
	}
	if optional, ok := backend.(objectrecords.OptionalPageQuery); ok {
		deps.Pages = optional
	}
	if optional, ok := backend.(objectrecords.OptionalURLQuery); ok {
		deps.URLPages = optional
	}
	if optional, ok := backend.(objectrecords.OptionalAuthorizedQuery); ok {
		deps.Authorized = optional
	}
	return objectrecords.NewService(deps)
}

func buildGen3Context(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func buildLocalAuthzContext(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func ptr[T any](value T) *T { return &value }

func registerCandidates(ctx context.Context, service *objectrecords.Service, candidates []objects.Candidate) (int, error) {
	records := make([]objects.Record, 0, len(candidates))
	for _, candidate := range candidates {
		record, err := objects.CandidateToRecord(candidate, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		records = append(records, record)
	}
	if err := service.RegisterObjects(ctx, records); err != nil {
		return 0, err
	}
	return len(records), nil
}

func newSQLiteDatabase(t *testing.T) *sqlite.SqliteDB {
	t.Helper()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	return database
}
