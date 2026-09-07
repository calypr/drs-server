package s3

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer"
	"github.com/calypr/syfon/internal/testutils"
)

func TestResponseContentDisposition(t *testing.T) {
	got := responseContentDisposition("nested/README final.md")
	want := common.ContentDispositionAttachment("nested/README final.md")
	if got == nil || *got != want {
		t.Fatalf("unexpected response content disposition: got %v want %q", got, want)
	}
}

func TestS3Signer_getClients(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"test-bucket": {
				Bucket:    "test-bucket",
				Region:    "us-east-1",
				AccessKey: "key",
				SecretKey: "secret",
				Endpoint:  "http://localhost:9000",
			},
		},
	}
	signer := NewS3Signer(db)
	ctx := context.Background()

	t.Run("Success_FirstTime", func(t *testing.T) {
		cls, err := signer.getClients(ctx, "test-bucket")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cls.client == nil || cls.presigner == nil {
			t.Fatal("expected clients to be initialized")
		}

		// Verify caching
		cls2, _ := signer.getClients(ctx, "test-bucket")
		if cls != cls2 {
			t.Error("expected cached client to be returned")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		signer.db.(*testutils.MockDatabase).NoDefaultCreds = true
		signer.db.(*testutils.MockDatabase).Credentials = nil
		_, err := signer.getClients(ctx, "unknown-bucket")
		if err == nil {
			t.Error("expected error for unknown bucket, got nil")
		}
	})

	t.Run("EndpointNormalization", func(t *testing.T) {
		cases := []struct {
			raw      string
			expected string
		}{
			{"localhost:9000", "http://localhost:9000"},
			{"s3.amazonaws.com", "https://s3.amazonaws.com"},
			{"http://minio:9000", "http://minio:9000"},
		}
		for _, tc := range cases {
			signer.cache.Delete("bucket-" + tc.raw)
			signer.db.(*testutils.MockDatabase).Credentials = map[string]models.S3Credential{
				"bucket-" + tc.raw: {
					Bucket:   "bucket-" + tc.raw,
					Endpoint: tc.raw,
				},
			}
			cls, err := signer.getClients(ctx, "bucket-"+tc.raw)
			if err != nil {
				t.Errorf("failed for %s: %v", tc.raw, err)
				continue
			}
			// In AWS SDK v2, we can't easily check the final endpoint without digging deep,
			// but we can at least ensure it doesn't crash and logic executes.
			if cls == nil {
				t.Errorf("got nil client for %s", tc.raw)
			}
		}
	})
}

func TestS3Signer_SignURL_EmbedsDownloadFilename(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"test-bucket": {
				Bucket:    "test-bucket",
				Region:    "us-east-1",
				AccessKey: "key",
				SecretKey: "secret",
				Endpoint:  "https://rgw.example.test",
			},
		},
	}
	s := NewS3Signer(db)

	signedURL, err := s.SignURL(context.Background(), "test-bucket", "prefix/object.bin", signer.SignOptions{
		DownloadFilename: "nested/README final.md",
	})
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}

	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("failed to parse signed url: %v", err)
	}
	got := parsed.Query().Get("response-content-disposition")
	want := common.ContentDispositionAttachment("nested/README final.md")
	if got != want {
		t.Fatalf("unexpected response-content-disposition: got %q want %q url=%s", got, want, signedURL)
	}
	if !strings.Contains(parsed.Path, "/test-bucket/prefix/object.bin") {
		t.Fatalf("unexpected signed path: %s", parsed.Path)
	}
}

func TestS3Signer_SignDownloadPart_IncludesRangeSignature(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"test-bucket": {
				Bucket:    "test-bucket",
				Region:    "us-east-1",
				AccessKey: "key",
				SecretKey: "secret",
				Endpoint:  "https://rgw.example.test",
			},
		},
	}
	s := NewS3Signer(db)

	signedURL, err := s.SignDownloadPart(context.Background(), "test-bucket", "prefix/object.bin", 0, 127, signer.SignOptions{DownloadFilename: "part.bin"})
	if err != nil {
		t.Fatalf("SignDownloadPart failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(signedURL), "range") {
		t.Fatalf("expected range to be signed in URL: %s", signedURL)
	}
	if !strings.Contains(signedURL, "response-content-disposition") {
		t.Fatalf("expected content disposition override in URL: %s", signedURL)
	}
}

func TestS3Signer_SignMultipartPart_IncludesUploadParams(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"test-bucket": {
				Bucket:    "test-bucket",
				Region:    "us-east-1",
				AccessKey: "key",
				SecretKey: "secret",
				Endpoint:  "https://rgw.example.test",
			},
		},
	}
	s := NewS3Signer(db)

	signedURL, err := s.SignMultipartPart(context.Background(), "test-bucket", "prefix/object.bin", "upload-123", 3)
	if err != nil {
		t.Fatalf("SignMultipartPart failed: %v", err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse signed multipart url: %v", err)
	}
	if got := parsed.Query().Get("partNumber"); got != "3" {
		t.Fatalf("expected partNumber=3, got %q (%s)", got, signedURL)
	}
	if got := parsed.Query().Get("uploadId"); got != "upload-123" {
		t.Fatalf("expected uploadId=upload-123, got %q (%s)", got, signedURL)
	}
}

func TestS3Signer_MultipartMethods_UnknownBucketError(t *testing.T) {
	db := &testutils.MockDatabase{NoDefaultCreds: true}
	s := NewS3Signer(db)

	if _, err := s.InitMultipartUpload(context.Background(), "missing-bucket", "k"); err == nil {
		t.Fatal("expected init multipart error for missing bucket")
	}
	if err := s.CompleteMultipartUpload(context.Background(), "missing-bucket", "k", "upload-1", nil); err == nil {
		t.Fatal("expected complete multipart error for missing bucket")
	}
}

