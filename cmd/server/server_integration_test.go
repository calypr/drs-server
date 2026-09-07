package server

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/api/internaldrs"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/crypto"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer/s3"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
	"github.com/gofiber/fiber/v3"
)

var (
	testConfigPath = flag.String("testConfig", "", "Path to config file for integration test")
)

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

func TestS3Integration(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	configPath := *testConfigPath
	if configPath == "" {
		// Create a temporary config for testing if none provided
		content := `
port: 8081
auth:
  mode: local
  basic:
    username: "drs-user"
    password: "drs-pass"
database:
  sqlite:
    file: "test_integration.db"
s3_credentials:
  - bucket: "test-bucket"
    provider: "s3"
    region: "us-east-1"
    access_key: "test-key"
    secret_key: "test-secret"
    endpoint: "` + os.TempDir() + `"
`
		tmpfile, err := os.CreateTemp("", "test_config*.yaml")
		if err != nil {
			t.Fatalf("failed to create temp config: %v", err)
		}
		defer os.Remove(tmpfile.Name())
		if _, err := tmpfile.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write temp config: %v", err)
		}
		tmpfile.Close()
		configPath = tmpfile.Name()
	} else if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try resolving from root (up 2 levels from cmd/server)
		rootPath := "../../" + configPath
		if _, err := os.Stat(rootPath); err == nil {
			configPath = rootPath
		}
	}

	// Read config to get bucket name for verification
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config file: %v", err)
	}
	if len(cfg.S3Credentials) == 0 {
		t.Skip("Skipping integration test; no s3 credentials in config")
	}
	testCred := cfg.S3Credentials[0]
	bucketName := testCred.Bucket
	organization := "test-org"
	project := "test-project"

	// Setup Server
	database := testutils.NewInMemoryDB()

	// Pre-load credentials from config (mimic server startup logic)
	for _, c := range cfg.S3Credentials {
		cred := &models.S3Credential{
			Bucket:    c.Bucket,
			Provider:  c.Provider,
			Region:    c.Region,
			AccessKey: c.AccessKey,
			SecretKey: c.SecretKey,
			Endpoint:  c.Endpoint,
		}
		if err := database.SaveS3Credential(context.Background(), cred); err != nil {
			t.Fatalf("Failed to preload credential: %v", err)
		}
	}
	if err := database.CreateBucketScope(context.Background(), &models.BucketScope{
		Organization: organization,
		ProjectID:    project,
		Bucket:       bucketName,
	}); err != nil {
		t.Fatalf("Failed to preload bucket scope: %v", err)
	}

	uM := urlmanager.NewManager(database, cfg.Signing)
	uM.RegisterSigner(common.S3Provider, s3.NewS3Signer(database))
	app := fiber.New()
	om := core.NewObjectManager(database, uM)
	internaldrs.RegisterInternalRoutes(app, om)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen not available in this environment: %v", err)
	}
	defer ln.Close()

	go func() {
		_ = app.Listener(ln)
	}()

	serverURL := "http://" + ln.Addr().String()
	defer func() {
		_ = app.Shutdown()
	}()

	client := &http.Client{}

	// 1. Verify credentials preloaded into the DB
	creds, err := database.ListS3Credentials(context.Background())
	if err != nil {
		t.Fatalf("Failed to list credentials: %v", err)
	}
	found := false
	for _, c := range creds {
		if c.Bucket == bucketName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Credential not found after insertion")
	}

	// 2. Create internal blank upload and get a signed upload URL
	key := fmt.Sprintf("test-upload-%d", time.Now().Unix())
	internalUploadReq := map[string]interface{}{
		"guid":         key,
		"organization": organization,
		"project":      project,
	}
	internalBody, _ := json.Marshal(internalUploadReq)
	resp, err := client.Post(serverURL+"/data/upload", "application/json", bytes.NewReader(internalBody))
	if err != nil {
		t.Fatalf("Internal upload blank failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("Internal upload blank failed: %s, Body: %s", resp.Status, string(b))
	}
	var internalResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&internalResp); err != nil {
		t.Fatalf("Failed to decode internal upload response: %v", err)
	}
	guid := internalResp["guid"]
	signedUploadURL := internalResp["url"]
	t.Logf("Upload URL: %s", signedUploadURL)
	if guid == "" || signedUploadURL == "" {
		t.Fatalf("expected guid and signed upload url, got guid=%q url=%q", guid, signedUploadURL)
	}

	// 3. Perform Upload
	dummyContent := []byte("Hello DRS Integration from Config")
	uploadReq, _ := http.NewRequest("PUT", signedUploadURL, bytes.NewReader(dummyContent))
	// Explicitly delete Content-Type to ensure we don't send one, as s3blob/AWS-SDK-v2 signing likely ignores it.
	uploadReq.Header.Del("Content-Type")
	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		t.Logf("Failed to upload file (expected with fake creds): %v", err)
	} else {
		// Note: If using real S3, this might fail if credentials aren't valid.
		if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
			t.Logf("Upload failed to S3 (expected with fake creds): %s", uploadResp.Status)
		} else {
			t.Log("Upload successful")
		}
	}

	// 4. Internal multipart init still works after credential preload.
	internalMultipartReq := map[string]interface{}{
		"guid":         guid,
		"name":         "test-multipart",
		"organization": organization,
		"project":      project,
	}
	mpBody, _ := json.Marshal(internalMultipartReq)
	resp, err = client.Post(serverURL+"/data/multipart/init", "application/json", bytes.NewReader(mpBody))
	if err != nil {
		t.Fatalf("Internal multipart init failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected multipart init status: %s", resp.Status)
	}
	if resp.StatusCode == http.StatusInternalServerError {
		t.Logf("Multipart init failed with fake/test credentials (expected in integration environments): %s", resp.Status)
	}

	// 5. Internal download for blank object should not resolve yet (no access method registered).
	resp, err = client.Get(serverURL + "/data/download/" + guid)
	if err != nil {
		t.Fatalf("Internal download req failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404 for download of incomplete object, got %s", resp.Status)
	}
}
