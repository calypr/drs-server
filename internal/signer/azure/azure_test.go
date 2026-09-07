package azure

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/google/uuid"
)

func TestAzureSASProtocol(t *testing.T) {
	if got := azureSASProtocol("http://localhost:10000/devstoreaccount1"); got != sas.ProtocolHTTPSandHTTP {
		t.Fatalf("expected HTTP endpoint to allow HTTP+HTTPS, got %v", got)
	}
	if got := azureSASProtocol("https://acct.blob.db.windows.net"); got != sas.ProtocolHTTPS {
		t.Fatalf("expected HTTPS endpoint to require HTTPS, got %v", got)
	}
	if got := azureSASProtocol("://bad-url"); got != sas.ProtocolHTTPS {
		t.Fatalf("expected invalid endpoint fallback to HTTPS, got %v", got)
	}
}

func TestAzureServiceURLAndAccountHelpers(t *testing.T) {
	s := &AzureSigner{}
	if got := s.azureServiceURL("acct", ""); got != "https://acct.blob.db.windows.net" {
		t.Fatalf("unexpected default azure service url: %s", got)
	}
	if got := s.azureServiceURL("", "localhost:10000/devstoreaccount1"); got != "https://localhost:10000/devstoreaccount1" {
		t.Fatalf("unexpected endpoint-normalized azure service url: %s", got)
	}

	if got := s.azureAccountFromEndpoint("http://localhost:10000/devstoreaccount1"); got != "localhost" {
		t.Fatalf("unexpected parsed account from localhost endpoint: %s", got)
	}
	if got := s.azureAccountFromEndpoint("https://myacct.blob.db.windows.net"); got != "myacct" {
		t.Fatalf("unexpected parsed account from azure endpoint: %s", got)
	}
	if got := s.azureAccountFromEndpoint("not a url"); got != "" {
		t.Fatalf("expected empty account for invalid endpoint, got %q", got)
	}
}

func TestAzureBlobURL_EscapesObjectPath(t *testing.T) {
	s := &AzureSigner{}
	got := s.azureBlobURL("https://acct.blob.db.windows.net", "bucket", "path with spaces/a+b.txt")
	want := "https://acct.blob.db.windows.net/bucket/path%20with%20spaces/a+b.txt"
	if got != want {
		t.Fatalf("unexpected blob URL:\n got: %s\nwant: %s", got, want)
	}
}

func TestAzureMultipartHelpers(t *testing.T) {
	s := &AzureSigner{}
	blockID := s.azureBlockID("upload-abc", 42)
	raw, err := base64.StdEncoding.DecodeString(blockID)
	if err != nil {
		t.Fatalf("failed to decode block ID: %v", err)
	}
	if got := string(raw); got != "upload-abc:00000042" {
		t.Fatalf("unexpected block ID payload: %s", got)
	}
}

func TestAzureSignedURL_UsesDownloadFilenameOverride(t *testing.T) {
	s := &AzureSigner{}
	sharedKey, err := azblob.NewSharedKeyCredential("acct", "dGVzdA==")
	if err != nil {
		t.Fatalf("create shared key: %v", err)
	}
	signed, err := s.azureSignedURL("https://acct.blob.db.windows.net", "bucket", "nested/object.txt", "GET", 5*time.Minute, "", "nested/report final.txt", sharedKey)
	if err != nil {
		t.Fatalf("azureSignedURL returned error: %v", err)
	}
	if !strings.Contains(signed, "rscd=") || !strings.Contains(signed, "report") {
		t.Fatalf("expected content disposition override in sas url: %s", signed)
	}
}

func TestAzureSigner_SignDownloadPart(t *testing.T) {
	db := &testutils.MockDatabase{Credentials: map[string]models.S3Credential{
		"test-bucket": {
			Bucket:    "test-bucket",
			AccessKey: "acct",
			SecretKey: "dGVzdA==",
			Endpoint:  "https://acct.blob.db.windows.net",
		},
	}}
	s := NewAzureSigner(db)

	signed, err := s.SignDownloadPart(context.Background(), "test-bucket", "nested/object.txt", 0, 512, signer.SignOptions{DownloadFilename: "chunk.txt"})
	if err != nil {
		t.Fatalf("SignDownloadPart returned error: %v", err)
	}
	if !strings.Contains(signed, "rscd=") || !strings.Contains(signed, "chunk") {
		t.Fatalf("expected content-disposition override in signed URL: %s", signed)
	}
}

func TestAzureSigner_MultipartHelpers(t *testing.T) {
	db := &testutils.MockDatabase{Credentials: map[string]models.S3Credential{
		"test-bucket": {
			Bucket:    "test-bucket",
			AccessKey: "acct",
			SecretKey: "dGVzdA==",
			Endpoint:  "https://acct.blob.db.windows.net",
		},
	}}
	s := NewAzureSigner(db)

	uploadID, err := s.InitMultipartUpload(context.Background(), "test-bucket", "obj.bin")
	if err != nil {
		t.Fatalf("InitMultipartUpload returned error: %v", err)
	}
	if _, err := uuid.Parse(uploadID); err != nil {
		t.Fatalf("expected UUID upload id, got %q err=%v", uploadID, err)
	}

	partURL, err := s.SignMultipartPart(context.Background(), "test-bucket", "obj.bin", uploadID, 2)
	if err != nil {
		t.Fatalf("SignMultipartPart returned error: %v", err)
	}
	if !strings.Contains(partURL, "comp=block") || !strings.Contains(partURL, "blockid=") {
		t.Fatalf("expected block upload query params in signed part URL: %s", partURL)
	}
}

