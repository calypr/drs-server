package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	syclient "github.com/calypr/syfon/client"
	"github.com/calypr/syfon/internal/config"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const postgresCompositionDSNEnv = "SYFON_TEST_POSTGRES_DSN"

func TestSyfonPostgresCompositionSurvivesRestart(t *testing.T) {
	postgresConfig := postgresCompositionConfig(t)
	binaryPath := buildSyfonBinary(t, findRepoRoot(t))
	storageRoot := t.TempDir()
	organization := "c13-" + uuid.NewString()
	project := "restart"
	bucket := "c13-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	objectID := "c13-restart-" + uuid.NewString()
	checksum := checksumForTest(objectID)
	accessURL := "file://" + filepath.Join(storageRoot, "objects", objectID)
	configPath := writePostgresCompositionConfig(t, postgresConfig, reserveTCPPort(t), storageRoot, bucket, organization, project)

	firstServer := startSyfonServerProcessWithBinary(t, binaryPath, configPath, nil)
	t.Cleanup(func() { stopSyfonServerProcess(t, firstServer) })
	client := postgresCompositionClient(t, firstServer)

	ctx := context.Background()
	if err := client.Health().Ping(ctx); err != nil {
		t.Fatalf("health check through public client: %v", err)
	}
	assertConfiguredScope(t, client, bucket, organization, project)
	created := createCompositionObject(t, client, objectID, checksum, accessURL, organization, project)
	assertObjectState(t, client, created.Did, checksum, accessURL)
	assertDRSObjectState(t, client, created.Did, checksum, accessURL)

	stopSyfonServerProcess(t, firstServer)

	configPath = writePostgresCompositionConfig(t, postgresConfig, reserveTCPPort(t), storageRoot, bucket, organization, project)
	secondServer := startSyfonServerProcessWithBinary(t, binaryPath, configPath, nil)
	t.Cleanup(func() { stopSyfonServerProcess(t, secondServer) })
	client = postgresCompositionClient(t, secondServer)

	assertConfiguredScope(t, client, bucket, organization, project)
	assertObjectState(t, client, created.Did, checksum, accessURL)
	assertDRSObjectState(t, client, created.Did, checksum, accessURL)

	if err := client.Index().Delete(ctx, created.Did); err != nil {
		t.Fatalf("delete durable object after restart: %v", err)
	}
	if _, err := client.Index().Get(ctx, created.Did); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("deleted object error = %v, want %v", err, errorapi.ErrNotFound)
	}
	if err := client.Buckets().Delete(ctx, bucket); err != nil {
		t.Fatalf("delete configured bucket after restart: %v", err)
	}
}

func TestSyfonPostgresCompositionConcurrentSameChecksum(t *testing.T) {
	postgresConfig := postgresCompositionConfig(t)
	binaryPath := buildSyfonBinary(t, findRepoRoot(t))
	storageRoot := t.TempDir()
	organization := "c13-" + uuid.NewString()
	project := "concurrent"
	bucket := "c13-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	configA := writePostgresCompositionConfig(t, postgresConfig, reserveTCPPort(t), storageRoot, bucket, organization, project)
	configB := writePostgresCompositionConfig(t, postgresConfig, reserveTCPPort(t), storageRoot, bucket, organization, project)

	serverA := startSyfonServerProcessWithBinary(t, binaryPath, configA, nil)
	t.Cleanup(func() { stopSyfonServerProcess(t, serverA) })
	serverB := startSyfonServerProcessWithBinary(t, binaryPath, configB, nil)
	t.Cleanup(func() { stopSyfonServerProcess(t, serverB) })
	clientA := postgresCompositionClient(t, serverA)
	clientB := postgresCompositionClient(t, serverB)

	objectA := "c13-a-" + uuid.NewString()
	objectB := "c13-b-" + uuid.NewString()
	checksum := checksumForTest(bucket + ":shared-content")
	accessURL := "file://" + filepath.Join(storageRoot, "objects", checksum)

	start := make(chan struct{})
	var (
		wait       sync.WaitGroup
		createdA   internalapi.InternalRecordResponse
		createdB   internalapi.InternalRecordResponse
		errA, errB error
	)
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		createdA, errA = createCompositionObjectResult(clientA, objectA, checksum, accessURL, organization, project)
	}()
	go func() {
		defer wait.Done()
		<-start
		createdB, errB = createCompositionObjectResult(clientB, objectB, checksum, accessURL, organization, project)
	}()
	close(start)
	wait.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("concurrent registration errors: A=%v B=%v", errA, errB)
	}

	ctx := context.Background()
	recordA, err := clientA.Index().Get(ctx, objectA)
	if err != nil {
		t.Fatalf("resolve first concurrent alias: %v", err)
	}
	recordB, err := clientB.Index().Get(ctx, objectB)
	if err != nil {
		t.Fatalf("resolve second concurrent alias: %v", err)
	}
	if strings.TrimSpace(recordA.Did) == "" || recordA.Did != recordB.Did {
		t.Fatalf("concurrent records resolved to different canonical IDs: A=%q B=%q (responses A=%q B=%q)", recordA.Did, recordB.Did, createdA.Did, createdB.Did)
	}
	assertObjectState(t, clientA, objectA, checksum, accessURL)
	assertObjectState(t, clientB, objectB, checksum, accessURL)
	if err := clientA.Index().Delete(ctx, objectA); err != nil {
		t.Fatalf("delete concurrent canonical object: %v", err)
	}
	if err := clientA.Buckets().Delete(ctx, bucket); err != nil {
		t.Fatalf("delete concurrent configured bucket: %v", err)
	}
}

func postgresCompositionConfig(t *testing.T) config.PostgresConfig {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(postgresCompositionDSNEnv))
	if dsn == "" {
		t.Skipf("%s is not configured", postgresCompositionDSNEnv)
	}
	parsed, err := pq.NewConfig(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", postgresCompositionDSNEnv, err)
	}
	if parsed.Host == "" || parsed.Database == "" || parsed.User == "" {
		t.Fatalf("%s must contain host, user, and database", postgresCompositionDSNEnv)
	}
	sslMode := string(parsed.SSLMode)
	if sslMode == "" {
		sslMode = "disable"
	}
	return config.PostgresConfig{
		Host:     parsed.Host,
		Port:     int(parsed.Port),
		User:     parsed.User,
		Password: parsed.Password,
		Database: parsed.Database,
		SSLMode:  sslMode,
	}
}

func writePostgresCompositionConfig(t *testing.T, database config.PostgresConfig, port int, storageRoot, bucket, organization, project string) string {
	t.Helper()
	content := fmt.Sprintf(`port: %d
auth:
  mode: local
  basic:
    username: %q
    password: %q
database:
  postgres:
    host: %q
    port: %d
    user: %q
    password: %q
    database: %q
    sslmode: %q
credential_encryption:
  master_key: %q
routes:
  ga4gh: true
  internal: true
  lfs: true
  metrics: false
  docs: false
buckets:
  - bucket: %q
    provider: file
    endpoint: %q
bucket_scopes:
  - bucket: %q
    organization: %q
    project_id: %q
`, port, dockerE2EBasicUser, dockerE2EBasicPass, database.Host, database.Port, database.User, database.Password, database.Database, database.SSLMode, dockerE2ECredentialKey, bucket, storageRoot, bucket, organization, project)
	configPath := filepath.Join(t.TempDir(), "postgres-composition.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write PostgreSQL composition config: %v", err)
	}
	return configPath
}

func postgresCompositionClient(t *testing.T, server *syfonServerProcess) *syclient.Client {
	t.Helper()
	address, err := url.Parse(server.url)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	address.User = nil
	client, err := syclient.New(address.String(), syclient.WithBasicAuth(dockerE2EBasicUser, dockerE2EBasicPass))
	if err != nil {
		t.Fatalf("initialize public client: %v", err)
	}
	return client
}

func createCompositionObject(t *testing.T, client *syclient.Client, objectID, checksum, accessURL, organization, project string) internalapi.InternalRecordResponse {
	t.Helper()
	created, err := createCompositionObjectResult(client, objectID, checksum, accessURL, organization, project)
	if err != nil {
		t.Fatalf("create object %s: %v", objectID, err)
	}
	return created
}

func createCompositionObjectResult(client *syclient.Client, objectID, checksum, accessURL, organization, project string) (internalapi.InternalRecordResponse, error) {
	accessMethods := []drsapi.AccessMethod{{
		Type:      drsapi.AccessMethodTypeFile,
		AccessUrl: &drsapi.AccessURL{Url: accessURL},
	}}
	return client.Index().Create(context.Background(), internalapi.InternalRecord{
		Did:           objectID,
		Organization:  c13StringPointer(organization),
		Project:       c13StringPointer(project),
		Hashes:        c13HashInfoPointer(checksum),
		AccessMethods: &accessMethods,
	})
}

func assertConfiguredScope(t *testing.T, client *syclient.Client, bucket, organization, project string) {
	t.Helper()
	scopes, err := client.Buckets().ListScopes(context.Background(), bucket)
	if err != nil {
		t.Fatalf("list configured bucket scopes: %v", err)
	}
	count := 0
	for _, scope := range scopes {
		if scope.Organization == organization && scope.ProjectId == project {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("configured scope count for %s/%s = %d, want 1 (all scopes: %+v)", organization, project, count, scopes)
	}
}

func assertObjectState(t *testing.T, client *syclient.Client, objectID, checksum, accessURL string) {
	t.Helper()
	record, err := client.Index().Get(context.Background(), objectID)
	if err != nil {
		t.Fatalf("get object %s: %v", objectID, err)
	}
	if record.Did == "" {
		t.Fatalf("object %s has empty canonical ID", objectID)
	}
	if record.Hashes == nil || (*record.Hashes)["sha256"] != checksum {
		t.Fatalf("object %s hashes = %#v, want sha256=%s", objectID, record.Hashes, checksum)
	}
	if !containsCompositionAccessURL(record.AccessMethods, accessURL) {
		t.Fatalf("object %s access methods = %#v, want %s", objectID, record.AccessMethods, accessURL)
	}
}

func assertDRSObjectState(t *testing.T, client *syclient.Client, objectID, checksum, accessURL string) {
	t.Helper()
	object, err := client.DRS().GetObject(context.Background(), objectID)
	if err != nil {
		t.Fatalf("get DRS object %s: %v", objectID, err)
	}
	if object.Id == "" || object.Did == nil || *object.Did != object.Id {
		t.Fatalf("DRS object identity = id %q did %#v", object.Id, object.Did)
	}
	foundChecksum := false
	for _, value := range object.Checksums {
		if value.Type == "sha256" && value.Checksum == checksum {
			foundChecksum = true
			break
		}
	}
	if !foundChecksum {
		t.Fatalf("DRS object %s checksums = %#v, want sha256=%s", objectID, object.Checksums, checksum)
	}
	if !containsCompositionAccessURL(object.AccessMethods, accessURL) {
		t.Fatalf("DRS object %s access methods = %#v, want %s", objectID, object.AccessMethods, accessURL)
	}
}

func containsCompositionAccessURL(methods *[]drsapi.AccessMethod, want string) bool {
	if methods == nil {
		return false
	}
	for _, method := range *methods {
		if method.AccessUrl != nil && method.AccessUrl.Url == want {
			return true
		}
	}
	return false
}

func checksumForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func c13StringPointer(value string) *string { return &value }

func c13HashInfoPointer(checksum string) *internalapi.HashInfo {
	hashes := internalapi.HashInfo{"sha256": checksum}
	return &hashes
}
