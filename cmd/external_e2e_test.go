package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestSyfonExternalServerE2E runs CLI-only e2e checks against a live Syfon server.
//
// Enable with:
//
//	SYFON_E2E_SERVER_URL=http://10.96.14.83 go test ./cmd -run TestSyfonExternalServerE2E -v
//
// Optional:
//
//	SYFON_E2E_EXPECTED_HEALTH_PATH=/healthz
func TestSyfonExternalServerE2E(t *testing.T) {
	serverURL := strings.TrimSpace(os.Getenv("SYFON_E2E_SERVER_URL"))
	if serverURL == "" {
		t.Skip("set SYFON_E2E_SERVER_URL to run external e2e test")
	}

	// 1. Ping
	out, err := executeRootCommand(t, "--server", serverURL, "ping")
	if err != nil {
		t.Fatalf("ping failed: %v output=%s", err, out)
	}
	if !strings.Contains(out, "Syfon is reachable") {
		t.Fatalf("unexpected ping output: %s", out)
	}

	// 2. Upload
	srcPath := filepath.Join(t.TempDir(), "external-e2e-source.txt")
	srcData := []byte("syfon external e2e payload")
	if err := os.WriteFile(srcPath, srcData, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	out, err = executeRootCommand(t, "--server", serverURL, "upload", "--file", srcPath, "--org", "syfon", "--project", "e2e")
	if err != nil {
		t.Fatalf("upload failed: %v output=%s", err, out)
	}
	uploadedID, err := parseUploadedObjectID(out)
	if err != nil {
		t.Fatalf("parse upload output: %v output=%s", err, out)
	}

	// 3. Download + compare bytes
	outPath := filepath.Join(t.TempDir(), "external-e2e-downloaded.txt")
	out, err = executeRootCommand(t, "--server", serverURL, "download", "--did", uploadedID, "--out", outPath)
	if err != nil {
		if bucket, ok := missingCredentialBucket(out); ok {
			bootstrapped, bootstrapErr := bootstrapExternalBucketCredential(serverURL, bucket)
			if bootstrapErr != nil {
				t.Skipf("external server missing credential for bucket %q and bootstrap failed: %v", bucket, bootstrapErr)
			}
			if !bootstrapped {
				t.Skipf(
					"external server missing credential for bucket %q. Set env vars (for example TEST_BUCKET_PROVIDER/ACCESS_KEY/SECRET_KEY/ENDPOINT/REGION) so test can bootstrap /data/buckets",
					bucket,
				)
			}
			out, err = executeRootCommand(t, "--server", serverURL, "download", "--did", uploadedID, "--out", outPath)
		}
	}
	if err != nil {
		t.Fatalf("download failed: %v output=%s", err, out)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, srcData) {
		t.Fatalf("downloaded bytes mismatch")
	}

	// 4. sha256sum updates record and prints the hash
	sumOut, err := executeRootCommand(t, "--server", serverURL, "sha256sum", "--did", uploadedID)
	if err != nil {
		t.Fatalf("sha256sum failed: %v output=%s", err, sumOut)
	}
	expectedHash := sha256.Sum256(srcData)
	expectedSum := hex.EncodeToString(expectedHash[:])
	if !strings.Contains(sumOut, expectedSum) {
		t.Fatalf("expected sha256 %s in output, got %s", expectedSum, sumOut)
	}
}

var uploadedIDRe = regexp.MustCompile(`(?:successfully\s+)?uploaded[:\s]+([a-f0-9-]{36,64})`)

func parseUploadedObjectID(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if m := uploadedIDRe.FindStringSubmatch(line); len(m) == 2 {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("missing uploaded line in output (regex match failed): %q", out)
}

var missingCredentialRe = regexp.MustCompile(`failed to get credentials for bucket ([^: ]+): credential not found`)

func missingCredentialBucket(output string) (string, bool) {
	m := missingCredentialRe.FindStringSubmatch(output)
	if len(m) == 2 && strings.TrimSpace(m[1]) != "" {
		return strings.TrimSpace(m[1]), true
	}
	return "", false
}

func bootstrapExternalBucketCredential(serverURL, bucket string) (bool, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return false, fmt.Errorf("bucket name is empty")
	}

	provider := firstNonEmptyEnv("TEST_BUCKET_PROVIDER", "SYFON_E2E_BUCKET_PROVIDER", "BUCKET_PROVIDER", "SYFON_BUCKET_PROVIDER")
	if provider == "" {
		provider = "s3"
	}
	region := firstNonEmptyEnv("TEST_BUCKET_REGION", "SYFON_E2E_BUCKET_REGION", "BUCKET_REGION", "AWS_REGION", "AWS_DEFAULT_REGION")
	if region == "" {
		region = "us-east-1"
	}
	endpoint := firstNonEmptyEnv("TEST_BUCKET_ENDPOINT", "SYFON_E2E_BUCKET_ENDPOINT", "BUCKET_ENDPOINT", "AWS_ENDPOINT_URL_S3", "S3_ENDPOINT")
	organization := firstNonEmptyEnv("TEST_ORGANIZATION", "SYFON_E2E_ORGANIZATION", "ORGANIZATION")
	projectID := firstNonEmptyEnv("TEST_PROJECT_ID", "SYFON_E2E_PROJECT_ID", "PROJECT_ID")
	if organization == "" {
		organization = "syfon"
	}
	if projectID == "" {
		projectID = "e2e"
	}
	accessKey := firstNonEmptyEnv("TEST_BUCKET_ACCESS_KEY", "SYFON_E2E_BUCKET_ACCESS_KEY", "BUCKET_ACCESS_KEY", "AWS_ACCESS_KEY_ID")
	secretKey := firstNonEmptyEnv("TEST_BUCKET_SECRET_KEY", "SYFON_E2E_BUCKET_SECRET_KEY", "BUCKET_SECRET_KEY", "AWS_SECRET_ACCESS_KEY")

	payload := map[string]string{
		"bucket":   bucket,
		"provider": provider,
		"region":   region,
	}
	if endpoint != "" {
		payload["endpoint"] = endpoint
	}
	if organization != "" {
		payload["organization"] = organization
	}
	if projectID != "" {
		payload["project_id"] = projectID
	}

	if accessKey == "" || secretKey == "" {
		return false, nil
	}
	payload["access_key"] = accessKey
	payload["secret_key"] = secretKey

	data, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal bucket payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, strings.TrimRight(serverURL, "/")+"/data/buckets", bytes.NewReader(data))
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("put /data/buckets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("put /data/buckets status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return true, nil
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}
