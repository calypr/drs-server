package postgres_test

import (
	"net/url"
	"strings"
	"testing"
)

func postgresTestSchemaDSN(t *testing.T, dsn, schema string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func TestPostgresSchemaDSN(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"host=localhost dbname=test", "host=localhost dbname=test search_path=isolated"},
		{"postgres://localhost/test?sslmode=disable", "postgres://localhost/test?search_path=isolated&sslmode=disable"},
		{"postgresql://localhost/test?search_path=public", "postgresql://localhost/test?search_path=isolated"},
	} {
		if got := postgresTestSchemaDSN(t, tc.input, "isolated"); got != tc.want {
			t.Errorf("schema DSN = %q, want %q", got, tc.want)
		}
	}
}
