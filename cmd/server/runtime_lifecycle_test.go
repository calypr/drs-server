package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/gofiber/fiber/v3"
	"github.com/spf13/cobra"
)

func TestServerRuntimeCloseClosesSQLiteDatabaseIdempotently(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	rt, err := buildServerRuntime(context.Background(), &config.Config{
		Database: config.DatabaseConfig{Sqlite: &config.SqliteConfig{File: ":memory:"}},
		Auth:     config.AuthConfig{Mode: config.AuthModeLocal},
	}, slog.Default())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	database := rt.database
	if database == nil || database.DB() == nil {
		t.Fatal("runtime did not retain its database")
	}
	if err := database.DB().Ping(); err != nil {
		t.Fatalf("ping before close: %v", err)
	}

	if err := rt.Close(context.Background()); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	if err := database.DB().Ping(); err == nil {
		t.Fatal("expected retained database to reject Ping after runtime close")
	}
	if err := rt.Close(context.Background()); err != nil {
		t.Fatalf("second close runtime: %v", err)
	}
}

func TestFailedRuntimeConstructionClosesSQLiteDatabase(t *testing.T) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		t.Skip("lsof is unavailable")
	}
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, strings.Repeat("a", 64))
	databasePath := filepath.Join(t.TempDir(), "failed-runtime.db")
	cfg := &config.Config{
		Database: config.DatabaseConfig{Sqlite: &config.SqliteConfig{File: databasePath}},
		BucketScopes: []config.BucketScopeConfig{{
			Organization: "org",
			ProjectID:    "project",
			Bucket:       "missing",
		}},
	}
	runtime, err := buildServerRuntime(context.Background(), cfg, slog.Default())
	if err == nil || runtime != nil {
		t.Fatalf("buildServerRuntime() = (%v, %v), want nil runtime and error", runtime, err)
	}
	openFiles, err := exec.Command(lsof, "-p", strconv.Itoa(os.Getpid()), "-Fn").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(openFiles), databasePath) {
		t.Fatalf("database remains open after failed runtime construction: %s", databasePath)
	}
}

func TestRuntimeCloseStopsOwnedListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	runtime := &serverRuntime{
		app:      fiber.New(),
		listener: &firstAcceptListener{Listener: listener, ready: ready},
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- runtime.app.Listener(runtime.listener) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("Fiber did not enter its serving loop")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(shutdownContext); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serveResult:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Listener() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Fiber listener did not stop")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestServeWithCancelledContextStartsAndStops(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cancelled-command.db")
	t.Setenv("DRS_PORT", "0")
	t.Setenv("DRS_DB_SQLITE_FILE", databasePath)
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_BASIC_AUTH_USER", "user")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "pass")
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, strings.Repeat("a", 64))
	previousConfigFile := configFile
	configFile = ""
	t.Cleanup(func() { configFile = previousConfigFile })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := &cobra.Command{}
	command.SetContext(ctx)
	result := make(chan error, 1)
	go func() { result <- Cmd.RunE(command, nil) }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve with cancelled context: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}
