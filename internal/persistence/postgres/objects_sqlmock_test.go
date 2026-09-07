package postgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/lib/pq"
)

func TestDeleteObject(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
			WithArgs("obj-1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("obj-1"))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT replace(lower(trim(checksum)), 'sha256:', '')")).
			WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"checksum"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).
			WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"resource"}))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object WHERE id = $1")).
			WithArgs("obj-1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		if err := pg.DeleteObject(context.Background(), "obj-1"); err != nil {
			t.Fatalf("DeleteObject returned error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT object_id FROM drs_object_alias WHERE alias_id = $1")).
			WithArgs("missing").WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()

		err := pg.DeleteObject(context.Background(), "missing")
		if !errors.Is(err, faults.ErrNotFound) {
			t.Fatalf("expected not found error, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("alias", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
			WithArgs("alias-1").
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT object_id FROM drs_object_alias WHERE alias_id = $1")).
			WithArgs("alias-1").
			WillReturnRows(sqlmock.NewRows([]string{"object_id"}).AddRow("obj-1"))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT replace(lower(trim(checksum)), 'sha256:', '')")).
			WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"checksum"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).
			WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"resource"}))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object WHERE id = $1")).
			WithArgs("obj-1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		if err := pg.DeleteObject(context.Background(), "alias-1"); err != nil {
			t.Fatalf("DeleteObject returned error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func TestGetObject_NotFound(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, size, created_time, updated_time, name, version, description
		FROM drs_object WHERE id = $1`)).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT object_id FROM drs_object_alias WHERE alias_id = $1")).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, err := pg.GetObject(context.Background(), "missing")
	if !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestGetObject_DeduplicatesAndPropagatesAuthz(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	updated := created.Add(2 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, size, created_time, updated_time, name, version, description
		FROM drs_object WHERE id = $1`)).
		WithArgs("obj-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "size", "created_time", "updated_time", "name", "version", "description",
		}).AddRow("obj-1", int64(123), created, updated, "file.txt", "v1", "desc"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT name_alias FROM drs_object_name_alias WHERE object_id = $1 ORDER BY name_alias")).
		WithArgs("obj-1").
		WillReturnRows(sqlmock.NewRows([]string{"name_alias"}).
			AddRow("file-old.txt"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT url, type FROM drs_object_access_method WHERE object_id = $1")).
		WithArgs("obj-1").
		WillReturnRows(sqlmock.NewRows([]string{"url", "type"}).
			AddRow("s3://bucket/key-1", "s3").
			AddRow("s3://bucket/key-1", "s3").
			AddRow("gs://bucket/key-2", "gs"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1 ORDER BY resource")).
		WithArgs("obj-1").
		WillReturnRows(sqlmock.NewRows([]string{"resource"}).
			AddRow("/programs/p1/projects/a").
			AddRow("/programs/p1/projects/b"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT public_read FROM drs_object_read_policy WHERE object_id = $1")).
		WithArgs("obj-1").WillReturnError(sql.ErrNoRows)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT type, checksum FROM drs_object_checksum WHERE object_id = $1")).
		WithArgs("obj-1").
		WillReturnRows(sqlmock.NewRows([]string{"type", "checksum"}).
			AddRow("sha256", "abc").
			AddRow("sha256", "abc").
			AddRow("md5", "def"))

	obj, err := pg.GetObject(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	if obj.Id != "obj-1" || obj.SelfUri != "drs://obj-1" {
		t.Fatalf("unexpected object identity fields: %+v", obj)
	}
	if obj.AccessMethods == nil || len(*obj.AccessMethods) != 2 {
		t.Fatalf("expected 2 deduplicated access methods, got %+v", obj.AccessMethods)
	}
	if len(obj.NameAliases) != 1 || obj.NameAliases[0] != "file-old.txt" {
		t.Fatalf("expected propagated name aliases, got %+v", obj.NameAliases)
	}
	if len(obj.Checksums) != 2 {
		t.Fatalf("expected 2 deduplicated checksums, got %d", len(obj.Checksums))
	}
	if got := obj.Authorizations["p1"]; len(got) != 2 {
		t.Fatalf("expected 2 deduplicated authz projects, got %+v", obj.Authorizations)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetObject_IgnoresAuthContext(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	now := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, size, created_time, updated_time, name, version, description
		FROM drs_object WHERE id = $1`)).
		WithArgs("obj-2").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "size", "created_time", "updated_time", "name", "version", "description",
		}).AddRow("obj-2", int64(1), now, now, "n", "v", "d"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT name_alias FROM drs_object_name_alias WHERE object_id = $1 ORDER BY name_alias")).
		WithArgs("obj-2").
		WillReturnRows(sqlmock.NewRows([]string{"name_alias"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT url, type FROM drs_object_access_method WHERE object_id = $1")).
		WithArgs("obj-2").
		WillReturnRows(sqlmock.NewRows([]string{"url", "type"}).
			AddRow("s3://bucket/key", "s3"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1 ORDER BY resource")).
		WithArgs("obj-2").
		WillReturnRows(sqlmock.NewRows([]string{"resource"}).AddRow("/programs/p1/projects/a"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT public_read FROM drs_object_read_policy WHERE object_id = $1")).
		WithArgs("obj-2").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT type, checksum FROM drs_object_checksum WHERE object_id = $1")).
		WithArgs("obj-2").
		WillReturnRows(sqlmock.NewRows([]string{"type", "checksum"}))
	session := access.NewSession("gen3")
	session.SetAuthorizations([]string{"/programs/p1/projects/other"}, nil, true)
	ctx := access.WithSession(context.Background(), session)

	obj, err := pg.GetObject(ctx, "obj-2")
	if err != nil {
		t.Fatalf("expected object fetch to ignore auth context, got %v", err)
	}
	if obj.Id != "obj-2" {
		t.Fatalf("expected obj-2, got %+v", obj)
	}
}

func TestGetBulkObjects_UsesSplitHydrationQueries(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	created := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)

	mock.ExpectQuery(`SELECT o\.id, o\.size, o\.created_time, o\.updated_time, o\.name, o\.version, o\.description FROM drs_object o WHERE`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "size", "created_time", "updated_time", "name", "version", "description",
		}).
			AddRow("obj-1", int64(10), created, updated, "file-1", "v1", "desc-1").
			AddRow("obj-2", int64(20), created, updated, "file-2", "v2", "desc-2"))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT object_id, url, type
		FROM drs_object_access_method
		WHERE object_id = ANY($1)
		ORDER BY object_id`)).
		WithArgs(pq.Array([]string{"obj-1", "obj-2"})).
		WillReturnRows(sqlmock.NewRows([]string{"object_id", "url", "type"}).
			AddRow("obj-1", "s3://bucket/a", "s3").
			AddRow("obj-1", "s3://bucket/a", "s3").
			AddRow("obj-2", "gs://bucket/b", "gs"))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT object_id, type, checksum
		FROM drs_object_checksum
		WHERE object_id = ANY($1)
		ORDER BY object_id`)).
		WithArgs(pq.Array([]string{"obj-1", "obj-2"})).
		WillReturnRows(sqlmock.NewRows([]string{"object_id", "type", "checksum"}).
			AddRow("obj-1", "sha256", "aaa").
			AddRow("obj-1", "sha256", "aaa").
			AddRow("obj-2", "md5", "bbb"))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT object_id, resource
		FROM drs_object_controlled_access
		WHERE object_id = ANY($1)
		ORDER BY object_id, resource`)).
		WithArgs(pq.Array([]string{"obj-1", "obj-2"})).
		WillReturnRows(sqlmock.NewRows([]string{"object_id", "resource"}).
			AddRow("obj-1", "/organization/org/project/p1").
			AddRow("obj-2", "/organization/org/project/p1"))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT object_id, public_read
		FROM drs_object_read_policy
		WHERE object_id = ANY($1)`)).
		WithArgs(pq.Array([]string{"obj-1", "obj-2"})).
		WillReturnRows(sqlmock.NewRows([]string{"object_id", "public_read"}))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT object_id, name_alias
		FROM drs_object_name_alias
		WHERE object_id = ANY($1)
		ORDER BY object_id, name_alias`)).
		WithArgs(pq.Array([]string{"obj-1", "obj-2"})).
		WillReturnRows(sqlmock.NewRows([]string{"object_id", "name_alias"}).
			AddRow("obj-1", "file-1-old").
			AddRow("obj-2", "file-2-old"))

	objects, err := pg.GetBulkObjects(context.Background(), []string{"obj-2", "obj-1", "obj-2"})
	if err != nil {
		t.Fatalf("GetBulkObjects returned error: %v", err)
	}
	if len(objects) != 2 || objects[0].Id != "obj-2" || objects[1].Id != "obj-1" {
		t.Fatalf("expected input order with deduplicated ids, got %+v", objects)
	}
	if objects[0].AccessMethods == nil || len(*objects[0].AccessMethods) != 1 {
		t.Fatalf("expected one access method on obj-2, got %+v", objects[0].AccessMethods)
	}
	if len(objects[0].NameAliases) != 1 || objects[0].NameAliases[0] != "file-2-old" {
		t.Fatalf("expected propagated aliases on obj-2, got %+v", objects[0].NameAliases)
	}
	if len(objects[1].Checksums) != 1 || objects[1].Checksums[0].Checksum != "aaa" {
		t.Fatalf("expected deduplicated checksum on obj-1, got %+v", objects[1].Checksums)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListScopedObjectIDsByChecksums(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT replace(lower(trim(c.checksum)), 'sha256:', ''), c.object_id
		FROM drs_object_checksum c
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = c.object_id
		WHERE ca.resource = $1 AND replace(lower(trim(c.type)), '-', '') = $2
		  AND replace(lower(trim(c.checksum)), 'sha256:', '') = ANY($3)
		ORDER BY 1, 2`)).
		WithArgs("/organization/org/project/p1", "sha256", pq.Array([]string{"sha-a", "sha-b", "missing"})).
		WillReturnRows(sqlmock.NewRows([]string{"checksum", "object_id"}).
			AddRow("sha-a", "obj-1").
			AddRow("sha-a", "obj-2").
			AddRow("sha-b", "obj-3"))

	res, err := pg.ListScopedObjectIDsByChecksums(context.Background(), "org", "p1", []string{"sha-a", "sha-a", "sha-b", "missing", ""})
	if err != nil {
		t.Fatalf("ListScopedObjectIDsByChecksums returned error: %v", err)
	}
	if got := res["sha-a"]; len(got) != 2 || got[0] != "obj-1" || got[1] != "obj-2" {
		t.Fatalf("unexpected ids for sha-a: %+v", got)
	}
	if got := res["sha-b"]; len(got) != 1 || got[0] != "obj-3" {
		t.Fatalf("unexpected ids for sha-b: %+v", got)
	}
	if got := res["missing"]; len(got) != 0 {
		t.Fatalf("expected empty ids for missing, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListObjectIDsByScopeOrgIncludesProjectScopes(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery("ca\\.resource = \\$1 OR ca\\.resource LIKE \\$2").
		WithArgs("/organization/calypr", "/organization/calypr/project/%").
		WillReturnRows(sqlmock.NewRows([]string{"object_id"}).
			AddRow("org-wide").
			AddRow("project-scoped"))

	ids, err := pg.ListObjectIDsByScope(context.Background(), "calypr", "")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope returned error: %v", err)
	}
	if len(ids) != 2 || ids[0] != "org-wide" || ids[1] != "project-scoped" {
		t.Fatalf("unexpected ids: %+v", ids)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresScopeResourceCondition(t *testing.T) {
	condition, args, err := postgresScopeResourceCondition("ca.resource", "org", "")
	if err != nil {
		t.Fatalf("postgresScopeResourceCondition returned error: %v", err)
	}
	if want := "(ca.resource = ? OR ca.resource LIKE ? ESCAPE '\\')"; condition != want {
		t.Fatalf("unexpected condition: got %q want %q", condition, want)
	}
	if len(args) != 2 || args[0] != "/organization/org" || args[1] != "/organization/org/project/%" {
		t.Fatalf("unexpected args: %+v", args)
	}

	condition, args, err = postgresScopeResourceCondition("ca.resource", "org", "project")
	if err != nil {
		t.Fatalf("postgresScopeResourceCondition returned error: %v", err)
	}
	if condition != "ca.resource = ?" {
		t.Fatalf("unexpected project condition: %q", condition)
	}
	if len(args) != 1 || args[0] != "/organization/org/project/project" {
		t.Fatalf("unexpected project args: %+v", args)
	}
}

func TestBulkDeleteObjects(t *testing.T) {
	t.Run("empty ids is noop", func(t *testing.T) {
		pg, _, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()
		if err := pg.BulkDeleteObjects(context.Background(), nil); err != nil {
			t.Fatalf("expected nil on empty ids, got %v", err)
		}
	})

	t.Run("deletes provided ids", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
		for _, id := range []string{"a", "b"} {
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).WithArgs(id).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).WithArgs(id).
				WillReturnRows(sqlmock.NewRows([]string{"resource"}))
		}
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object WHERE id = ANY($1)")).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectCommit()

		if err := pg.BulkDeleteObjects(context.Background(), []string{"a", "b"}); err != nil {
			t.Fatalf("BulkDeleteObjects returned error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func TestUpdateObjectAccessMethods(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
		WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("obj-1"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).
		WithArgs("obj-1").WillReturnRows(sqlmock.NewRows([]string{"resource"}))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object_access_method WHERE object_id = $1")).
		WithArgs("obj-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO drs_object_access_method (object_id, url, type) VALUES ($1, $2, $3)")).
		WithArgs("obj-1", "s3://bucket/key", "s3").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := pg.UpdateObjectAccessMethods(context.Background(), "obj-1", []objects.AccessMethod{{
		Type:      "s3",
		AccessUrl: &objects.AccessURL{Url: "s3://bucket/key"},
	}})
	if err != nil {
		t.Fatalf("UpdateObjectAccessMethods returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBulkUpdateAccessMethods(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).WillReturnResult(sqlmock.NewResult(0, 0))
	for _, id := range []string{"obj-1", "obj-2"} {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
			WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).
			WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"resource"}))
	}
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object_access_method WHERE object_id = $1")).
		WithArgs("obj-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO drs_object_access_method (object_id, url, type) VALUES ($1, $2, $3)")).
		WithArgs("obj-1", "s3://bucket/key", "s3").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object_access_method WHERE object_id = $1")).
		WithArgs("obj-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := pg.BulkUpdateAccessMethods(context.Background(), map[string][]objects.AccessMethod{
		"obj-1": {
			{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/key"},
			},
		},
		"obj-2": {
			{Type: "s3"}, // no URL, should be skipped after delete
		},
	})
	if err != nil {
		t.Fatalf("BulkUpdateAccessMethods returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListObjectIDsPageByURL(t *testing.T) {
	t.Run("scope and cursor", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT o.id
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		WHERE am.url = $1 AND EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca_scope
			WHERE ca_scope.object_id = o.id AND ca_scope.resource = $2
		) AND o.id > $3
		ORDER BY o.id
		LIMIT $4 OFFSET $5`)).
			WithArgs("s3://bucket/path/image.offsets.json", "/organization/org/project/p1", "obj-a", 10, 2).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("obj-b").AddRow("obj-c"))

		ids, err := pg.ListObjectIDsPageByURL(
			context.Background(),
			"s3://bucket/path/image.offsets.json",
			"org",
			"p1",
			"obj-a",
			10,
			2,
			nil,
			false,
			false,
		)
		if err != nil {
			t.Fatalf("ListObjectIDsPageByURL returned error: %v", err)
		}
		if !slices.Equal(ids, []string{"obj-b", "obj-c"}) {
			t.Fatalf("unexpected URL IDs: %v", ids)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("resource filter includes unscoped", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT o.id
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		WHERE am.url = $1 AND (
			(
				COALESCE(array_length($2::text[], 1), 0) > 0
				AND EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY($2)
				)
			) OR (
				$3
				AND NOT EXISTS (
					SELECT 1
					FROM drs_object_controlled_access ca_auth
					WHERE ca_auth.object_id = o.id
				)
			)
		)
		ORDER BY o.id
		LIMIT $4 OFFSET $5`)).
			WithArgs("s3://bucket/path/image.offsets.json", sqlmock.AnyArg(), true, 5, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("obj-b").AddRow("obj-c"))

		ids, err := pg.ListObjectIDsPageByURL(
			context.Background(),
			"s3://bucket/path/image.offsets.json",
			"",
			"",
			"",
			5,
			0,
			[]string{"/programs/org/projects/p2"},
			true,
			true,
		)
		if err != nil {
			t.Fatalf("ListObjectIDsPageByURL returned error: %v", err)
		}
		if !slices.Equal(ids, []string{"obj-b", "obj-c"}) {
			t.Fatalf("unexpected URL IDs: %v", ids)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}
