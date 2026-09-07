package postgres

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestListBucketVisibilityRows_RestrictsAndHydratesRows(t *testing.T) {
	t.Run("empty restricted resources are a no-op", func(t *testing.T) {
		pg, _, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		rows, err := pg.ListBucketVisibilityRows(context.Background(), nil, false, true)
		if err != nil {
			t.Fatalf("ListBucketVisibilityRows returned error: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("expected no rows, got %+v", rows)
		}
	})

	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	mock.ExpectQuery("SELECT DISTINCT am.url, am.type, COALESCE\\(ca.resource, ''\\)").
		WithArgs(pq.Array([]string{"/organization/org/project/project"}), true).
		WillReturnRows(sqlmock.NewRows([]string{"url", "type", "resource"}).
			AddRow("s3://bucket/object", "s3", "/organization/org/project/project").
			AddRow("gs://bucket/object", "gs", ""))

	rows, err := pg.ListBucketVisibilityRows(context.Background(), []string{"/organization/org/project/project"}, true, true)
	if err != nil {
		t.Fatalf("ListBucketVisibilityRows returned error: %v", err)
	}
	if len(rows) != 2 || rows[0].AccessURL != "s3://bucket/object" || rows[1].Resource != "" {
		t.Fatalf("unexpected visibility rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
