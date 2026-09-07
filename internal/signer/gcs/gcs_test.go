package gcs

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer"
	"github.com/calypr/syfon/internal/testutils"
)

func TestGCSEndpointObjectURL(t *testing.T) {
	cred := &models.S3Credential{Endpoint: "http://localhost:4443"}

	uploadURL, ok := gcsEndpointObjectURL(cred, "test-bucket", "path/to/file.txt", http.MethodPut, "")
	if !ok {
		t.Fatal("expected upload endpoint URL")
	}
	up, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	if got, want := up.Path, "/upload/storage/v1/b/test-bucket/o"; got != want {
		t.Fatalf("unexpected upload path: got %q want %q", got, want)
	}
	if got := up.Query().Get("uploadType"); got != "media" {
		t.Fatalf("expected uploadType=media, got %q", got)
	}
	if got := up.Query().Get("name"); got != "path/to/file.txt" {
		t.Fatalf("expected upload name to preserve object key, got %q", got)
	}

	downloadURL, ok := gcsEndpointObjectURL(cred, "test-bucket", "path/to/file.txt", http.MethodGet, "nested/pretty-name.txt")
	if !ok {
		t.Fatal("expected download endpoint URL")
	}
	dl, err := url.Parse(downloadURL)
	if err != nil {
		t.Fatalf("parse download url: %v", err)
	}
	if got, want := dl.Path, "/storage/v1/b/test-bucket/o/path%2Fto%2Ffile.txt"; got != want {
		t.Fatalf("unexpected download path: got %q want %q", got, want)
	}
	if got := dl.Query().Get("alt"); got != "media" {
		t.Fatalf("expected alt=media, got %q", got)
	}
	if got := dl.Query().Get("response-content-disposition"); !strings.Contains(got, `pretty-name.txt`) {
		t.Fatalf("expected response-content-disposition override, got %q", got)
	}
}

func TestGCSEndpointObjectURL_RequiresEndpoint(t *testing.T) {
	if _, ok := gcsEndpointObjectURL(&models.S3Credential{}, "bucket", "obj", http.MethodGet, ""); ok {
		t.Fatal("expected false when endpoint is missing")
	}
}

func TestGCSSignedURL_UsesEndpointWithoutServiceAccountKey(t *testing.T) {
	cred := &models.S3Credential{Endpoint: "http://localhost:4443"}
	s := &GCSSigner{}
	signed, err := s.gcsSignedURL("test-bucket", "nested/file.txt", http.MethodGet, 5*time.Minute, "", "nested/report.txt", cred)
	if err != nil {
		t.Fatalf("gcsSignedURL returned error: %v", err)
	}
	if !strings.Contains(signed, "/storage/v1/b/test-bucket/o/nested%252Ffile.txt") {
		t.Fatalf("unexpected signed endpoint url: %s", signed)
	}
	if !strings.Contains(signed, "alt=media") {
		t.Fatalf("expected media download query in signed endpoint url: %s", signed)
	}
	if !strings.Contains(signed, "response-content-disposition=") || !strings.Contains(signed, "report.txt") {
		t.Fatalf("expected download filename override in signed endpoint url: %s", signed)
	}
}

func TestGCSSigner_SignDownloadPart_EndpointMode(t *testing.T) {
	s := NewGCSSigner(&testutils.MockDatabase{Credentials: map[string]models.S3Credential{
		"test-bucket": {
			Bucket:   "test-bucket",
			Endpoint: "http://localhost:4443",
		},
	}})

	signed, err := s.SignDownloadPart(context.Background(), "test-bucket", "nested/file.txt", 0, 255, signer.SignOptions{DownloadFilename: "chunk.txt"})
	if err != nil {
		t.Fatalf("SignDownloadPart returned error: %v", err)
	}
	if !strings.Contains(signed, "/storage/v1/b/test-bucket/o/nested%252Ffile.txt") {
		t.Fatalf("unexpected signed download-part URL: %s", signed)
	}
	if !strings.Contains(signed, "response-content-disposition=") {
		t.Fatalf("expected content-disposition in signed download-part URL: %s", signed)
	}
}

func TestGCSSigner_MultipartHelpers(t *testing.T) {
	s := NewGCSSigner(&testutils.MockDatabase{Credentials: map[string]models.S3Credential{
		"test-bucket": {
			Bucket:   "test-bucket",
			Endpoint: "http://localhost:4443",
		},
	}})

	uploadID, err := s.InitMultipartUpload(context.Background(), "test-bucket", "obj.bin")
	if err != nil {
		t.Fatalf("InitMultipartUpload returned error: %v", err)
	}
	if strings.TrimSpace(uploadID) == "" {
		t.Fatal("expected non-empty upload id")
	}

	partURL, err := s.SignMultipartPart(context.Background(), "test-bucket", "obj.bin", uploadID, 4)
	if err != nil {
		t.Fatalf("SignMultipartPart returned error: %v", err)
	}
	if !strings.Contains(partURL, "uploadType=media") || !strings.Contains(partURL, "name=") {
		t.Fatalf("expected endpoint upload semantics in signed part URL: %s", partURL)
	}
}

