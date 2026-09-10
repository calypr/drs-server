package httpapi

import (
	"context"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type testDRSServicesFixture struct {
	objectService   *objects.Service
	transferService *transfers.Service
}

func testDRSServices(objectStore objects.ObjectStore, storageAccess transfers.StoragePort) *testDRSServicesFixture {
	objectService := objects.NewService(objectStore)
	return &testDRSServicesFixture{
		objectService: objectService,
		transferService: transfers.NewService(transfers.Dependencies{
			Objects: objectService,
			Storage: storageAccess,
			Events:  drsTestTransferEvents{},
		}),
	}
}

type drsObjectStore struct {
	*store.Store
}

func newDRSObjectStore(t testing.TB, records map[string]*drs.DrsObject) *drsObjectStore {
	t.Helper()
	database, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seed := make([]drs.DrsObject, 0, len(records))
	for _, record := range records {
		seed = append(seed, *record)
	}
	if err := database.RegisterObjects(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	return &drsObjectStore{Store: database}
}

type drsTestTransferEvents struct{}

func (drsTestTransferEvents) RecordTransferAttributionEvents(context.Context, []usage.Event) error {
	return nil
}

var (
	_ objects.ObjectStore     = (*drsObjectStore)(nil)
	_ transfers.EventRecorder = drsTestTransferEvents{}
)
