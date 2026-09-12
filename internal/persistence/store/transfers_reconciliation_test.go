package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
	postgresdb "github.com/calypr/syfon/internal/persistence/postgres"
	sqlitedb "github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/usage"
	"github.com/google/uuid"
)

func TestProviderTransferReconciliationUsesPhysicalURLFallback(t *testing.T) {
	for _, backend := range []struct {
		name string
		open func(*testing.T) *store.Store
	}{
		{
			name: "sqlite",
			open: func(t *testing.T) *store.Store {
				db, err := sqlitedb.NewSqliteDB(":memory:", nil)
				if err != nil {
					t.Fatalf("open sqlite test store: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				return db
			},
		},
		{
			name: "postgres",
			open: func(t *testing.T) *store.Store {
				dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
				}
				db, err := postgresdb.NewPostgresDB(dsn, nil)
				if err != nil {
					t.Fatalf("open postgres test store: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				return db
			},
		},
	} {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.open(t)
			ctx := context.Background()
			placeholder := "?"
			if backend.name == "postgres" {
				placeholder = "$1"
			}
			suffix := uuid.NewString()
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			grant := func(name, storageURL string) usage.Event {
				return usage.Event{
					EventID:        "reconcile-grant-" + name + "-" + suffix,
					EventType:      usage.TransferEventAccessIssued,
					Direction:      usage.ProviderTransferDirectionDownload,
					EventTime:      now,
					ObjectID:       "object-" + name + "-" + suffix,
					SHA256:         "sha-" + name + "-" + suffix,
					ObjectSize:     10,
					AccessID:       "s3",
					Provider:       "s3",
					Bucket:         "physical-" + suffix,
					StorageURL:     storageURL,
					BytesRequested: 10,
				}
			}
			grants := []usage.Event{
				grant("exact", "s3://physical-"+suffix+"/exact-key"),
				grant("url", "s3://logical-"+suffix+"/url-key"),
				grant("fallback", "s3://logical-"+suffix+"/fallback-key"),
				grant("ambiguous-a", "s3://logical-"+suffix+"/ambiguous-key"),
				grant("ambiguous-b", "s3://logical-"+suffix+"/ambiguous-key"),
			}
			if err := db.RecordTransferAttributionEvents(ctx, grants); err != nil {
				t.Fatalf("RecordTransferAttributionEvents: %v", err)
			}
			grantIDs := make(map[string]string, len(grants))
			for _, event := range grants {
				grantIDs[event.EventID] = usage.GrantID(event)
			}

			physicalBucket := "physical-" + suffix
			providerURL := func(key string) *string {
				value := "s3://" + physicalBucket + "/" + key
				return &value
			}
			objectKey := func(value string) *string { return &value }
			events := []struct {
				name       string
				event      metricsapi.ProviderTransferEvent
				wantStatus string
				wantGrant  string
			}{
				{
					name: "exact URL wins",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-exact-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(time.Minute)),
						Provider:         "s3",
						Bucket:           physicalBucket,
						ObjectKey:        objectKey("not-exact-key"),
						StorageUrl:       providerURL("exact-key"),
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferMatched,
					wantGrant:  grantIDs[grants[0].EventID],
				},
				{
					name: "physical URL with explicit key",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-fallback-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(2 * time.Minute)),
						Provider:         "s3",
						Bucket:           physicalBucket,
						ObjectKey:        objectKey("fallback-key"),
						StorageUrl:       providerURL("fallback-key"),
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferMatched,
					wantGrant:  grantIDs[grants[2].EventID],
				},
				{
					name: "physical URL derives key",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-url-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(3 * time.Minute)),
						Provider:         "s3",
						Bucket:           physicalBucket,
						StorageUrl:       providerURL("url-key"),
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferMatched,
					wantGrant:  grantIDs[grants[1].EventID],
				},
				{
					name: "ambiguous key",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-ambiguous-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(4 * time.Minute)),
						Provider:         "s3",
						Bucket:           physicalBucket,
						ObjectKey:        objectKey("ambiguous-key"),
						StorageUrl:       providerURL("ambiguous-key"),
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferAmbiguous,
				},
				{
					name: "no reliable key",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-unmatched-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(5 * time.Minute)),
						Provider:         "s3",
						Bucket:           physicalBucket,
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferUnmatched,
				},
				{
					name: "provider is exact",
					event: metricsapi.ProviderTransferEvent{
						ProviderEventId:  "provider-wrong-provider-" + suffix,
						Direction:        metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
						EventTime:        transferReconciliationTimePtr(now.Add(6 * time.Minute)),
						Provider:         "gcs",
						Bucket:           physicalBucket,
						ObjectKey:        objectKey("fallback-key"),
						StorageUrl:       providerURL("fallback-key"),
						BytesTransferred: 10,
					},
					wantStatus: usage.ProviderTransferUnmatched,
				},
			}
			for _, testCase := range events {
				t.Run(testCase.name, func(t *testing.T) {
					if err := db.RecordProviderTransferEvents(ctx, []metricsapi.ProviderTransferEvent{testCase.event}); err != nil {
						t.Fatalf("RecordProviderTransferEvents: %v", err)
					}
					var status, accessGrantID string
					query := `SELECT reconciliation_status, access_grant_id FROM provider_transfer_event WHERE provider_event_id = ` + placeholder
					if err := db.DB().QueryRowContext(ctx, query, testCase.event.ProviderEventId).Scan(&status, &accessGrantID); err != nil {
						t.Fatalf("read provider transfer event: %v", err)
					}
					if status != testCase.wantStatus {
						t.Fatalf("reconciliation status = %q, want %q", status, testCase.wantStatus)
					}
					if accessGrantID != testCase.wantGrant {
						t.Fatalf("access grant ID = %q, want %q", accessGrantID, testCase.wantGrant)
					}
				})
			}

			replay := events[1].event
			if err := db.RecordProviderTransferEvents(ctx, []metricsapi.ProviderTransferEvent{replay}); err != nil {
				t.Fatalf("replay RecordProviderTransferEvents: %v", err)
			}
			var count int
			query := `SELECT COUNT(*) FROM provider_transfer_event WHERE provider_event_id = ` + placeholder
			if err := db.DB().QueryRowContext(ctx, query, replay.ProviderEventId).Scan(&count); err != nil {
				t.Fatalf("count replayed provider event: %v", err)
			}
			if count != 1 {
				t.Fatalf("replayed provider event count = %d, want 1", count)
			}
		})
	}
}

func transferReconciliationTimePtr(value time.Time) *time.Time { return &value }
