package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/api/docs"
	"github.com/calypr/syfon/internal/api/drsapi"
	"github.com/calypr/syfon/internal/api/internaldrs"
	"github.com/calypr/syfon/internal/api/metrics"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer/file"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
	"github.com/gofiber/fiber/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func executeRootCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetCommandFlags(RootCmd)
	clearRootAuthFlags(t, RootCmd)
	var out bytes.Buffer
	var errOut bytes.Buffer
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	RootCmd.SetArgs(args)
	err := RootCmd.Execute()
	return strings.TrimSpace(out.String() + errOut.String()), err
}

func TestSyfonMetricsTransfersCLI(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	now := time.Now().UTC()
	if err := server.DB.RecordTransferAttributionEvents(context.Background(), []models.TransferAttributionEvent{
		{
			EventID:        "cli-grant-1",
			AccessGrantID:  "cli-grant-1",
			EventType:      models.TransferEventAccessIssued,
			Direction:      models.ProviderTransferDirectionDownload,
			EventTime:      now.Add(-time.Minute),
			ObjectID:       "did-cli-1",
			SHA256:         "sha-cli-1",
			ObjectSize:     123,
			Organization:   "syfon",
			Project:        "e2e",
			AccessID:       "s3",
			Provider:       "file",
			Bucket:         "syfon-bucket",
			StorageURL:     "s3://syfon-bucket/sha-cli-1",
			BytesRequested: 123,
			ActorEmail:     "user@example.com",
			ActorSubject:   "user@example.com",
		},
		{
			EventID:        "cli-grant-2",
			AccessGrantID:  "cli-grant-2",
			EventType:      models.TransferEventAccessIssued,
			Direction:      models.ProviderTransferDirectionUpload,
			EventTime:      now.Add(-30 * time.Second),
			ObjectID:       "did-cli-2",
			SHA256:         "sha-cli-2",
			ObjectSize:     50,
			Organization:   "syfon",
			Project:        "e2e",
			AccessID:       "s3",
			Provider:       "file",
			Bucket:         "syfon-bucket",
			StorageURL:     "s3://syfon-bucket/sha-cli-2",
			BytesRequested: 50,
			ActorEmail:     "user@example.com",
			ActorSubject:   "user@example.com",
		},
		{
			EventID:        "cli-grant-3",
			AccessGrantID:  "cli-grant-3",
			EventType:      models.TransferEventAccessIssued,
			Direction:      models.ProviderTransferDirectionDownload,
			EventTime:      now.Add(-20 * time.Second),
			ObjectID:       "did-cli-3",
			SHA256:         "sha-cli-3",
			ObjectSize:     7,
			Organization:   "syfon",
			Project:        "e2e",
			AccessID:       "s3",
			Provider:       "file",
			Bucket:         "syfon-bucket",
			StorageURL:     "s3://syfon-bucket/sha-cli-3",
			BytesRequested: 7,
			ActorEmail:     "other@example.com",
			ActorSubject:   "other@example.com",
		},
	}); err != nil {
		t.Fatalf("record access grant: %v", err)
	}
	if err := server.DB.RecordProviderTransferEvents(context.Background(), []models.ProviderTransferEvent{
		{
			ProviderEventID:      "cli-transfer-1",
			AccessGrantID:        "cli-grant-1",
			Direction:            models.ProviderTransferDirectionDownload,
			EventTime:            now,
			ObjectID:             "did-cli-1",
			SHA256:               "sha-cli-1",
			Organization:         "syfon",
			Project:              "e2e",
			Provider:             "file",
			Bucket:               "syfon-bucket",
			StorageURL:           "s3://syfon-bucket/sha-cli-1",
			BytesTransferred:     123,
			ActorEmail:           "user@example.com",
			ActorSubject:         "user@example.com",
			ReconciliationStatus: models.ProviderTransferMatched,
		},
		{
			ProviderEventID:      "cli-transfer-2",
			AccessGrantID:        "cli-grant-3",
			Direction:            models.ProviderTransferDirectionDownload,
			EventTime:            now.Add(10 * time.Second),
			ObjectID:             "did-cli-3",
			SHA256:               "sha-cli-3",
			Organization:         "syfon",
			Project:              "e2e",
			Provider:             "file",
			Bucket:               "syfon-bucket",
			StorageURL:           "s3://syfon-bucket/sha-cli-3",
			BytesTransferred:     7,
			ActorEmail:           "other@example.com",
			ActorSubject:         "other@example.com",
			ReconciliationStatus: models.ProviderTransferMatched,
		},
	}); err != nil {
		t.Fatalf("record transfer event: %v", err)
	}
	syncFrom := now.Add(-2 * time.Minute).Format(time.RFC3339)
	syncTo := now.Add(2 * time.Minute).Format(time.RFC3339)

	out, err := executeRootCommand(t, "--server", server.URL, "metrics", "transfers", "summary", "--organization", "syfon", "--project", "e2e", "--user", "user@example.com", "--provider", "file", "--bucket", "syfon-bucket", "--from", syncFrom, "--to", syncTo)
	if err != nil {
		t.Fatalf("metrics transfers summary command failed: %v output=%s", err, out)
	}
	var summary models.TransferAttributionSummary
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("decode summary output %q: %v", out, err)
	}
	if summary.EventCount != 2 || summary.BytesDownloaded != 123 || summary.BytesUploaded != 50 {
		t.Fatalf("unexpected summary output: %+v", summary)
	}
	if summary.Freshness == nil || summary.Freshness.IsStale {
		t.Fatalf("expected summary output to include freshness metadata, got %+v", summary.Freshness)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "metrics", "transfers", "breakdown", "--organization", "syfon", "--project", "e2e", "--provider", "file", "--bucket", "syfon-bucket", "--from", syncFrom, "--to", syncTo, "--limit", "2")
	if err != nil {
		t.Fatalf("metrics transfers breakdown command failed: %v output=%s", err, out)
	}
	var breakdown struct {
		GroupBy   string                                `json:"group_by"`
		Data      []models.TransferAttributionBreakdown `json:"data"`
		Freshness *models.TransferMetricsFreshness      `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(out), &breakdown); err != nil {
		t.Fatalf("decode breakdown output %q: %v", out, err)
	}
	if breakdown.GroupBy != "user" || len(breakdown.Data) != 2 || breakdown.Data[0].Key != "user@example.com" || breakdown.Data[0].BytesDownloaded != 123 || breakdown.Data[1].Key != "other@example.com" {
		t.Fatalf("unexpected breakdown output: %+v", breakdown)
	}
	if breakdown.Freshness == nil || breakdown.Freshness.IsStale {
		t.Fatalf("expected breakdown output to include freshness metadata, got %+v", breakdown.Freshness)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "metrics", "transfers", "users", "--organization", "syfon", "--project", "e2e", "--provider", "file", "--bucket", "syfon-bucket", "--from", syncFrom, "--to", syncTo, "--sort-by", "downloaded")
	if err != nil {
		t.Fatalf("metrics transfers users command failed: %v output=%s", err, out)
	}
	var users struct {
		Summary models.TransferAttributionSummary `json:"summary"`
		Users   []struct {
			User            string `json:"user"`
			BytesDownloaded int64  `json:"bytes_downloaded"`
			BytesUploaded   int64  `json:"bytes_uploaded"`
		} `json:"users"`
		SortBy    string                           `json:"sort_by"`
		SortOrder string                           `json:"sort_order"`
		Freshness *models.TransferMetricsFreshness `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		t.Fatalf("decode users output %q: %v", out, err)
	}
	if users.SortBy != "downloaded" || users.SortOrder != "desc" {
		t.Fatalf("unexpected users sort metadata: %+v", users)
	}
	if len(users.Users) != 2 || users.Users[0].User != "user@example.com" || users.Users[0].BytesDownloaded != 123 || users.Users[1].User != "other@example.com" {
		t.Fatalf("unexpected users output: %+v", users.Users)
	}
	if users.Summary.BytesDownloaded != 130 || users.Summary.BytesUploaded != 50 {
		t.Fatalf("unexpected users summary output: %+v", users.Summary)
	}
	if users.Freshness == nil || users.Freshness.IsStale {
		t.Fatalf("expected users output to include freshness metadata, got %+v", users.Freshness)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "metrics", "transfers", "billing", "--organization", "syfon", "--project", "e2e", "--user", "user@example.com", "--from", syncFrom, "--to", syncTo)
	if err != nil {
		t.Fatalf("metrics transfers billing command failed: %v output=%s", err, out)
	}
	var billing struct {
		Summary          models.TransferAttributionSummary     `json:"summary"`
		StorageLocations []models.TransferAttributionBreakdown `json:"storage_locations"`
		Files            []models.TransferAttributionBreakdown `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &billing); err != nil {
		t.Fatalf("decode billing output %q: %v", out, err)
	}
	if billing.Summary.BytesDownloaded != 123 || billing.Summary.BytesUploaded != 50 {
		t.Fatalf("unexpected billing summary: %+v", billing.Summary)
	}
	if len(billing.StorageLocations) != 1 || billing.StorageLocations[0].Provider != "file" || billing.StorageLocations[0].Bucket != "syfon-bucket" {
		t.Fatalf("expected storage-location billing row, got %+v", billing.StorageLocations)
	}
	if len(billing.Files) != 2 {
		t.Fatalf("expected file-level billing rows, got %+v", billing.Files)
	}
}

func resetCommandFlags(cmd *cobra.Command) {
	resetFlagSet(cmd.PersistentFlags())
	resetFlagSet(cmd.Flags())
	for _, child := range cmd.Commands() {
		resetCommandFlags(child)
	}
}

func resetFlagSet(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

func clearRootAuthFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()

	for _, name := range []string{"profile", "token", "username", "password"} {
		flag := cmd.PersistentFlags().Lookup(name)
		if flag == nil {
			continue
		}
		if err := flag.Value.Set(""); err != nil {
			t.Fatalf("reset %s flag: %v", name, err)
		}
		flag.Changed = false
	}
}

type fiberTestServer struct {
	URL        string
	StorageDir string
	DB         db.DatabaseInterface
	app        *fiber.App
	ln         net.Listener
}

func (s *fiberTestServer) Close() {
	_ = s.app.Shutdown()
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

func newSyfonTestServer(t *testing.T) *fiberTestServer {
	t.Helper()

	kek := filepath.Join(t.TempDir(), ".syfon-credential-kek")
	t.Setenv("DRS_CREDENTIAL_LOCAL_KEY_FILE", kek)
	t.Setenv("SYFON_CREDENTIAL_KEK_DIR", kek)
	// start server...

	storageDir := t.TempDir()

	database := testutils.NewInMemoryDB()
	if err := database.SaveS3Credential(context.Background(), &models.S3Credential{
		Bucket:   "syfon-bucket",
		Provider: "file",
		Endpoint: storageDir,
	}); err != nil {
		t.Fatalf("save test credential: %v", err)
	}
	if err := database.CreateBucketScope(context.Background(), &models.BucketScope{
		Organization: "syfon",
		ProjectID:    "e2e",
		Bucket:       "syfon-bucket",
	}); err != nil {
		t.Fatalf("save test bucket scope: %v", err)
	}

	uM := urlmanager.NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	fSigner, _ := file.NewFileSigner(storageDir)
	uM.RegisterSigner(common.FileProvider, fSigner)

	app := fiber.New()
	app.Get(config.RouteHealthz, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})
	api := app.Group("/")
	om := core.NewObjectManager(database, uM)

	drsAPI := api.Group("/ga4gh/drs/v1")
	drsapi.RegisterDRSRoutes(drsAPI, om)
	docs.RegisterSwaggerRoutes(app)
	metrics.RegisterMetricsRoutes(api, database)
	internaldrs.RegisterInternalRoutes(api, om)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen not available in this environment: %v", err)
	}
	go func() {
		_ = app.Listener(ln)
	}()

	return &fiberTestServer{
		URL:        "http://" + ln.Addr().String(),
		StorageDir: storageDir,
		DB:         database,
		app:        app,
		ln:         ln,
	}
}

func TestSyfonVersionAndPing(t *testing.T) {
	server := newSyfonTestServer(t)
	defer server.Close()

	out, err := executeRootCommand(t, "--server", server.URL, "version")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if !strings.Contains(out, "Syfon ") {
		t.Fatalf("unexpected version output: %s", out)
	}

	out, err = executeRootCommand(t, "--server", server.URL, "ping")
	if err != nil {
		t.Fatalf("ping command failed: %v", err)
	}
	if !strings.Contains(out, "Syfon is reachable") {
		t.Fatalf("unexpected ping output: %s", out)
	}
}
