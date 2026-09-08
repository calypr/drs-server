package records_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
)

func newTestService(backend any, _ ...any) *objectrecords.Service {
	return objectrecords.NewService(backend.(objectrecords.ObjectStore))
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

func newSQLiteDatabase(t *testing.T) *store.Store {
	t.Helper()
	cipher, err := credentialcipher.NewFromEnv()
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	database, err := sqlite.NewSqliteDB(":memory:", cipher)
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	return database
}
