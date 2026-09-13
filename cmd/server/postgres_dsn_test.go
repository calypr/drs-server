package server

import (
	"testing"

	"github.com/calypr/syfon/internal/config"
	"github.com/lib/pq"
)

func TestPostgresDSNRoundTripsConfiguration(t *testing.T) {
	want := config.PostgresConfig{
		Host:     "::1",
		Port:     5433,
		User:     "user@domain",
		Password: "p#ss?/: %世界",
		Database: "db/name ?%",
		SSLMode:  "verify-full",
	}
	parsed, err := pq.NewConfig(postgresDSN(want))
	if err != nil {
		t.Fatalf("parse generated PostgreSQL DSN: %v", err)
	}
	if parsed.Host != want.Host || int(parsed.Port) != want.Port || parsed.User != want.User || parsed.Password != want.Password || parsed.Database != want.Database || string(parsed.SSLMode) != want.SSLMode {
		t.Fatalf("parsed PostgreSQL config = {host:%q port:%d user:%q password:%q database:%q sslmode:%q}, want %+v", parsed.Host, parsed.Port, parsed.User, parsed.Password, parsed.Database, parsed.SSLMode, want)
	}
}
