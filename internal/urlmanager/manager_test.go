package urlmanager

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/crypto"
	"github.com/calypr/syfon/internal/db/sqlite"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer/azure"
	"github.com/calypr/syfon/internal/signer/file"
	"github.com/calypr/syfon/internal/signer/s3"
)

func TestManager_SignURL(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	cred := &models.S3Credential{
		Bucket:    "my-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
	}
	if err := database.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))

	urlStr := "s3://my-bucket/my-obj"
	signedURL, err := manager.SignURL(ctx, "resource-1", urlStr, SignOptions{})
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}

	if !strings.Contains(signedURL, "my-bucket") || !strings.Contains(signedURL, "my-obj") {
		t.Errorf("expected signed URL to contain bucket and key, got: %s", signedURL)
	}

	if !strings.Contains(signedURL, "X-Amz-Signature") {
		t.Errorf("expected signed URL to contain signature, got: %s", signedURL)
	}

	uploadURL, err := manager.SignUploadURL(ctx, "resource-1", urlStr, SignOptions{})
	if err != nil {
		t.Fatalf("SignUploadURL failed: %v", err)
	}
	if !strings.Contains(uploadURL, "my-bucket") || !strings.Contains(uploadURL, "X-Amz-Signature") {
		t.Errorf("expected upload URL to contain bucket and signature, got: %s", uploadURL)
	}
}

func TestManager_FileScheme(t *testing.T) {
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	root := t.TempDir()
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:   "local-bucket",
		Provider: "file",
		Endpoint: root,
	}); err != nil {
		t.Fatalf("failed to save file credential: %v", err)
	}
	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	fSigner, _ := file.NewFileSigner(root)
	manager.RegisterSigner("file", fSigner)

	urlStr := "s3://local-bucket/test.txt"
	signedURL, err := manager.SignURL(ctx, "local-bucket", urlStr, SignOptions{})
	if err != nil {
		t.Fatalf("SignURL failed for file-backed bucket: %v", err)
	}
	if signedURL != filepath.ToSlash(filepath.Join(root, "test.txt")) {
		t.Errorf("expected raw filesystem path, got: %s", signedURL)
	}
}

func TestManager_MultipartMethods(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:    "mp-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
		Endpoint:  "http://127.0.0.1:1", // Unreachable endpoint for init/complete tests
	}); err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))

	// Presigning upload part is local.
	partURL, err := manager.SignMultipartPart(ctx, "mp-bucket", "obj", "upload-id", 1)
	if err != nil {
		t.Fatalf("SignMultipartPart failed: %v", err)
	}
	if !strings.Contains(partURL, "partNumber=1") {
		t.Fatalf("expected part URL with part number, got %s", partURL)
	}

	// Init/complete hit S3 API; against an unreachable endpoint they should fail cleanly.
	if _, err := manager.InitMultipartUpload(ctx, "mp-bucket", "obj"); err == nil {
		t.Fatal("expected InitMultipartUpload to fail against unreachable endpoint")
	}
}

func TestManager_ResolveFallbackFromAccessIDToURLBucket(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:    "cbds",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
	}); err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))

	signedURL, err := manager.SignURL(ctx, "s3", "s3://cbds/path/to/object", SignOptions{})
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}
	if !strings.Contains(signedURL, "cbds") || !strings.Contains(signedURL, "X-Amz-Signature") {
		t.Fatalf("unexpected signed url: %s", signedURL)
	}
}

func TestManager_AzureSignURLAndUploadURL(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:    "az-container",
		Provider:  "azure",
		AccessKey: "devstoreaccount1",
		SecretKey: "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==",
		Endpoint:  "http://127.0.0.1:10000/devstoreaccount1",
	}); err != nil {
		t.Fatalf("failed to save azure credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("azure", azure.NewAzureSigner(database))

	signedURL, err := manager.SignURL(ctx, "", "azblob://az-container/path/to/object.bin", SignOptions{})
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}
	parsedGet, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse signed get url: %v", err)
	}
	if got, want := parsedGet.Path, "/devstoreaccount1/az-container/path/to/object.bin"; got != want {
		t.Fatalf("unexpected signed get path: got %q want %q", got, want)
	}
	if parsedGet.Query().Get("sig") == "" {
		t.Fatalf("expected signed get URL to include sig query parameter: %s", signedURL)
	}

	signedUploadURL, err := manager.SignUploadURL(ctx, "", "azblob://az-container/path/to/object.bin", SignOptions{})
	if err != nil {
		t.Fatalf("SignUploadURL failed: %v", err)
	}
	parsedPut, err := url.Parse(signedUploadURL)
	if err != nil {
		t.Fatalf("parse signed put url: %v", err)
	}
	if got, want := parsedPut.Path, "/devstoreaccount1/az-container/path/to/object.bin"; got != want {
		t.Fatalf("unexpected signed put path: got %q want %q", got, want)
	}
	if parsedPut.Query().Get("sig") == "" {
		t.Fatalf("expected signed put URL to include sig query parameter: %s", signedUploadURL)
	}
}

func TestManager_SignDownloadPart_S3(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:    "download-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
	}); err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))
	signed, err := manager.SignDownloadPart(ctx, "", "s3://download-bucket/path/to/object.bin", 0, 9, SignOptions{})
	if err != nil {
		t.Fatalf("SignDownloadPart failed: %v", err)
	}
	if !strings.Contains(signed, "X-Amz-Signature") {
		t.Fatalf("expected presigned URL, got %s", signed)
	}
}

func TestManager_CompleteMultipartUpload_FailsOnUnreachableS3(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	if err := database.SaveS3Credential(ctx, &models.S3Credential{
		Bucket:    "mp-complete-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
		Endpoint:  "http://127.0.0.1:1",
	}); err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))
	err = manager.CompleteMultipartUpload(ctx, "mp-complete-bucket", "obj", "upload-id", []MultipartPart{{PartNumber: 1, ETag: "etag1"}})
	if err == nil {
		t.Fatal("expected CompleteMultipartUpload to fail against unreachable endpoint")
	}
}

func TestManager_InvalidateBucketRefreshesSignerCache(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	cred := &models.S3Credential{
		Bucket:    "ceph-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "test-key-id",
		SecretKey: "test-secret-key",
		Endpoint:  "https://rgw-a.example.org",
	}
	if err := database.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("failed to save initial credential: %v", err)
	}

	manager := NewManager(database, config.SigningConfig{DefaultExpirySeconds: 900})
	manager.RegisterSigner("s3", s3.NewS3Signer(database))

	signedA, err := manager.SignURL(ctx, "", "s3://ceph-bucket/path/to/object.bin", SignOptions{})
	if err != nil {
		t.Fatalf("initial SignURL failed: %v", err)
	}
	parsedA, err := url.Parse(signedA)
	if err != nil {
		t.Fatalf("parse initial signed url: %v", err)
	}
	if got, want := parsedA.Host, "rgw-a.example.org"; got != want {
		t.Fatalf("unexpected initial host: got %q want %q", got, want)
	}

	cred.Endpoint = "https://rgw-b.example.org"
	if err := database.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("failed to save updated credential: %v", err)
	}

	signedStale, err := manager.SignURL(ctx, "", "s3://ceph-bucket/path/to/object.bin", SignOptions{})
	if err != nil {
		t.Fatalf("stale SignURL failed: %v", err)
	}
	parsedStale, err := url.Parse(signedStale)
	if err != nil {
		t.Fatalf("parse stale signed url: %v", err)
	}
	if got, want := parsedStale.Host, "rgw-a.example.org"; got != want {
		t.Fatalf("expected cached host before invalidation: got %q want %q", got, want)
	}

	manager.InvalidateBucket("ceph-bucket")

	signedB, err := manager.SignURL(ctx, "", "s3://ceph-bucket/path/to/object.bin", SignOptions{})
	if err != nil {
		t.Fatalf("refreshed SignURL failed: %v", err)
	}
	parsedB, err := url.Parse(signedB)
	if err != nil {
		t.Fatalf("parse refreshed signed url: %v", err)
	}
	if got, want := parsedB.Host, "rgw-b.example.org"; got != want {
		t.Fatalf("unexpected refreshed host: got %q want %q", got, want)
	}
}
