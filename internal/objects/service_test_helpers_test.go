package objects_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

func newTestService(backend any, _ ...any) *objects.Service {
	deps := objects.Dependencies{
		Reader:        backend.(objects.RecordReader),
		Writer:        backend.(objects.RecordWriter),
		AccessMethods: backend.(objects.AccessMethodWriter),
		AccessPolicy:  backend.(objects.AccessPolicyWriter),
		Aliases:       backend.(objects.AliasStore),
		Content:       backend.(objects.ContentReader),
		ChecksumScope: backend.(objects.ChecksumScopeQuery),
		Scope:         backend.(objects.ScopeQuery),
	}
	if optional, ok := backend.(objects.OptionalResourceQuery); ok {
		deps.Resources = optional
	}
	if optional, ok := backend.(objects.OptionalPageQuery); ok {
		deps.Pages = optional
	}
	if optional, ok := backend.(objects.OptionalURLQuery); ok {
		deps.URLPages = optional
	}
	if optional, ok := backend.(objects.OptionalAuthorizedQuery); ok {
		deps.Authorized = optional
	}
	return objects.NewService(deps)
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

func registerCandidates(ctx context.Context, service *objects.Service, candidates []objects.Candidate) (int, error) {
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
