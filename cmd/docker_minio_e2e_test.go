package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/calypr/syfon/internal/crypto"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	dockerE2EEnvVar          = "SYFON_E2E_DOCKER"
	dockerE2EMinioImage      = "minio/minio:RELEASE.2025-03-12T18-04-18Z"
	dockerE2EMinioBucket     = "syfon-e2e-bucket"
	dockerE2EMinioRegion     = "us-east-1"
	dockerE2EMinioAccessKey  = "minioadmin"
	dockerE2EMinioSecretKey  = "minioadmin123"
	dockerE2ECredentialKey   = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	dockerE2EServerReadyWait = 20 * time.Second
	dockerE2EBasicUser       = "drs-user"
	dockerE2EBasicPass       = "drs-pass"
)

type minioContainer struct {
	container testcontainers.Container
	endpoint  string
	bucket    string
	region    string
	accessKey string
	secretKey string
	s3Client  *s3.Client
}

type syfonServerProcess struct {
	url       string
	cmd       *exec.Cmd
	waitErrCh <-chan error
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
}

func TestSyfonDockerMinIOE2E(t *testing.T) {
	if strings.TrimSpace(os.Getenv(dockerE2EEnvVar)) != "1" {
		t.Skipf("set %s=1 to run the Docker-backed MinIO integration test", dockerE2EEnvVar)
	}

	ctx := context.Background()
	t.Logf("STEP 1: Starting MinIO Docker container...")
	minioEnv, err := startMinIOContainer(ctx)
	if err != nil {
		if isDockerUnavailable(err) {
			t.Logf("Docker unavailable for %s: %v", dockerE2EEnvVar, err)
			t.Skipf("Docker is unavailable for %s: %v", dockerE2EEnvVar, err)
		}
		t.Fatalf("FAILED to start MinIO container: %v", err)
	}
	t.Logf("MinIO started at %s (Bucket: %s)", minioEnv.endpoint, minioEnv.bucket)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := minioEnv.container.Terminate(cleanupCtx); err != nil {
			t.Logf("Warning: failed to terminate MinIO container: %v", err)
		}
	})

	t.Logf("STEP 2: Starting Syfon server process...")
	server := startSyfonServerProcess(t, minioEnv)
	t.Logf("Syfon server listening at %s", server.url)
	t.Cleanup(func() {
		stopSyfonServerProcess(t, server)
	})

	t.Logf("STEP 3: Pinging Syfon server...")
	pingOut, err := executeRootCommand(t, "--server", server.url, "ping")
	t.Logf("Ping Output:\n%s", pingOut)
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if !strings.Contains(pingOut, "Syfon is reachable") {
		t.Fatalf("Unexpected ping output: (missing 'Syfon is reachable')")
	}

	t.Logf("STEP 4: Uploading test file...")
	srcPath := filepath.Join(t.TempDir(), "docker-minio-source.txt")
	srcData := []byte("syfon docker minio e2e payload")
	if err := os.WriteFile(srcPath, srcData, 0o644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	uploadOut, err := executeRootCommand(t, "--server", server.url, "upload", "--file", srcPath, "--org", "syfon", "--project", "e2e")
	t.Logf("Upload Output:\n%s", uploadOut)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	uploadedID, err := parseUploadedObjectID(uploadOut)
	if err != nil {
		t.Fatalf("Failed to parse DID from upload output: %v", err)
	}
	expectedHash := sha256.Sum256(srcData)
	expectedSum := hex.EncodeToString(expectedHash[:])
	t.Logf("Object registered with canonical DID: %s (storage key: %s)", uploadedID, expectedSum)

	t.Logf("STEP 5: Verifying object existence in MinIO directly...")
	if _, err := minioEnv.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(minioEnv.bucket),
		Key:    aws.String(expectedSum),
	}); err != nil {
		t.Fatalf("Data check failed: Object is missing from MinIO bucket: %v", err)
	}
	t.Logf("Object verified in S3 bucket.")

	t.Logf("STEP 6: Downloading object...")
	downloadPath := filepath.Join(t.TempDir(), "docker-minio-downloaded.txt")
	downloadOut, err := executeRootCommand(t, "--server", server.url, "download", "--did", uploadedID, "--out", downloadPath)
	t.Logf("Download Output:\n%s", downloadOut)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	got, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, srcData) {
		t.Fatalf("Integrity Check Failed: downloaded bytes mismatch")
	}
	t.Logf("Download verified (bytes match source).")

	t.Logf("STEP 7: Verifying hash computation...")
	sumOut, err := executeRootCommand(t, "--server", server.url, "sha256sum", "--did", uploadedID)
	t.Logf("Sha256sum Output:\n%s", sumOut)
	if err != nil {
		t.Fatalf("sha256sum command failed: %v", err)
	}
	if !strings.Contains(sumOut, expectedSum) {
		t.Fatalf("Hash mismatch: expected %s, got output: %s", expectedSum, sumOut)
	}
	t.Logf("Hash verified.")
}

func startMinIOContainer(ctx context.Context) (*minioContainer, error) {
	request := testcontainers.ContainerRequest{
		Image:        dockerE2EMinioImage,
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     dockerE2EMinioAccessKey,
			"MINIO_ROOT_PASSWORD": dockerE2EMinioSecretKey,
		},
		Cmd:        []string{"server", "/data", "--address", ":9000"},
		WaitingFor: wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: request,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())
	s3Client, err := newMinIOClient(ctx, endpoint)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	if err := ensureMinIOBucket(ctx, s3Client, dockerE2EMinioBucket); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	return &minioContainer{
		container: container,
		endpoint:  endpoint,
		bucket:    dockerE2EMinioBucket,
		region:    dockerE2EMinioRegion,
		accessKey: dockerE2EMinioAccessKey,
		secretKey: dockerE2EMinioSecretKey,
		s3Client:  s3Client,
	}, nil
}

func newMinIOClient(ctx context.Context, endpoint string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(dockerE2EMinioRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(dockerE2EMinioAccessKey, dockerE2EMinioSecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	cfg.BaseEndpoint = aws.String(endpoint)
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	}), nil
}

func ensureMinIOBucket(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "bucketalready") {
		return fmt.Errorf("create bucket %s: %w", bucket, err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for bucket %s: %w", bucket, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func startSyfonServerProcess(t *testing.T, minioEnv *minioContainer) *syfonServerProcess {
	t.Helper()

	rootDir := findRepoRoot(t)
	binaryPath := buildSyfonBinary(t, rootDir)
	port := reserveTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "docker-minio-e2e.db")
	configPath := writeSyfonDockerConfig(t, port, dbPath, minioEnv)
	serverURL := fmt.Sprintf("http://%s:%s@127.0.0.1:%d", dockerE2EBasicUser, dockerE2EBasicPass, port)
	readyURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(binaryPath, "serve", "--config", configPath)
	cmd.Dir = rootDir
	cmd.Env = append(os.Environ(), crypto.CredentialMasterKeyEnv+"="+dockerE2ECredentialKey)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	// Multi-writer to keep buffers for error reporting and stream to T.Log for live feedback.
	go streamToTestLog(t, "[SERVER STDOUT]", stdoutPipe, stdoutBuf)
	go streamToTestLog(t, "[SERVER STDERR]", stderrPipe, stderrBuf)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start syfon server: %v", err)
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()

	if err := waitForServerReady(readyURL, waitErrCh, dockerE2EServerReadyWait); err != nil {
		logServerProcessOutput(t, readyURL, stdoutBuf, stderrBuf)
		stopSyfonServerProcess(t, &syfonServerProcess{cmd: cmd, waitErrCh: waitErrCh, stdout: stdoutBuf, stderr: stderrBuf})
		t.Fatalf("wait for server ready: %v", err)
	}

	return &syfonServerProcess{
		url:       serverURL,
		cmd:       cmd,
		waitErrCh: waitErrCh,
		stdout:    stdoutBuf,
		stderr:    stderrBuf,
	}
}

func stopSyfonServerProcess(t *testing.T, server *syfonServerProcess) {
	t.Helper()
	if server == nil || server.cmd == nil || server.cmd.Process == nil {
		return
	}
	if server.cmd.ProcessState != nil {
		return
	}

	_ = syscall.Kill(-server.cmd.Process.Pid, syscall.SIGINT)
	select {
	case <-server.waitErrCh:
		return
	case <-time.After(5 * time.Second):
	}

	_ = server.cmd.Process.Kill()
	select {
	case <-server.waitErrCh:
	case <-time.After(5 * time.Second):
		logServerProcessOutput(t, server.url, server.stdout, server.stderr)
		t.Fatalf("server process did not exit cleanly")
	}
}

func waitForServerReady(baseURL string, waitErrCh <-chan error, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	const requiredConsecutiveSuccesses = 2
	successes := 0
	interval := 100 * time.Millisecond

	for {
		select {
		case err := <-waitErrCh:
			return fmt.Errorf("server exited before ready: %w", err)
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for /healthz after %s", timeout)
		default:
		}

		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				successes++
				if successes >= requiredConsecutiveSuccesses {
					return nil
				}
			} else {
				successes = 0
			}
		} else {
			successes = 0
		}

		timer := time.NewTimer(interval)
		select {
		case err := <-waitErrCh:
			timer.Stop()
			return fmt.Errorf("server exited before ready: %w", err)
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out waiting for /healthz after %s", timeout)
		case <-timer.C:
		}
		if interval < time.Second {
			interval *= 2
			if interval > time.Second {
				interval = time.Second
			}
		}
	}
}

func writeSyfonDockerConfig(t *testing.T, port int, dbPath string, minioEnv *minioContainer) string {
	t.Helper()

	return writeScopedProviderConfig(t, providerServerConfig{
		Port:         port,
		DBPath:       dbPath,
		Bucket:       minioEnv.bucket,
		Provider:     "s3",
		Region:       minioEnv.region,
		AccessKey:    minioEnv.accessKey,
		SecretKey:    minioEnv.secretKey,
		Endpoint:     minioEnv.endpoint,
		Organization: "syfon",
		ProjectID:    "e2e",
	})
}

func buildSyfonBinary(t *testing.T, rootDir string) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "syfon-docker-e2e")
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = rootDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build syfon binary: %v\n%s", err, string(out))
	}
	return binaryPath
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod root from %s", dir)
		}
		dir = parent
	}
}

func logServerProcessOutput(t *testing.T, serverURL string, stdoutBuf, stderrBuf *bytes.Buffer) {
	t.Helper()
	if stdoutBuf != nil {
		t.Logf("server %s stdout:\n%s", serverURL, stdoutBuf.String())
	}
	if stderrBuf != nil {
		t.Logf("server %s stderr:\n%s", serverURL, stderrBuf.String())
	}
}

func streamToTestLog(t *testing.T, prefix string, r io.Reader, buf *bytes.Buffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		t.Logf("%s %s", prefix, line)
		if buf != nil {
			buf.WriteString(line + "\n")
		}
	}
}

func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "docker daemon") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "cannot connect") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "rootless docker not found") ||
		strings.Contains(lower, "failed to create docker provider") ||
		strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "failed to create container")
}
func TestSyfonDockerMultipartUpload(t *testing.T) {
	if strings.TrimSpace(os.Getenv(dockerE2EEnvVar)) != "1" {
		t.Skipf("set %s=1 to run the Docker-backed MinIO integration test", dockerE2EEnvVar)
	}

	ctx := context.Background()
	t.Logf("STEP 1: Starting MinIO Docker container for Multipart test...")
	minioEnv, err := startMinIOContainer(ctx)
	if err != nil {
		if isDockerUnavailable(err) {
			t.Logf("Docker unavailable for %s: %v", dockerE2EEnvVar, err)
			t.Skipf("Docker is unavailable for %s: %v", dockerE2EEnvVar, err)
		}
		t.Fatalf("FAILED to start MinIO container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = minioEnv.container.Terminate(cleanupCtx)
	})

	t.Logf("STEP 2: Starting Syfon server process...")
	server := startSyfonServerProcess(t, minioEnv)
	t.Cleanup(func() { stopSyfonServerProcess(t, server) })

	t.Logf("STEP 3: Generating 7MB junk file for multipart check...")
	srcPath := filepath.Join(t.TempDir(), "large-multipart-source.txt")
	const sizeMB = 7
	srcData := make([]byte, sizeMB*1024*1024)
	for i := range srcData {
		srcData[i] = byte(i % 256)
	}
	if err := os.WriteFile(srcPath, srcData, 0o644); err != nil {
		t.Fatalf("Failed to write large source file: %v", err)
	}

	t.Logf("STEP 4: Executing Multipart Upload (7MB)...")
	uploadOut, err := executeRootCommand(t, "--server", server.url, "upload", "--file", srcPath, "--org", "syfon", "--project", "e2e")
	t.Logf("Multipart Upload Output:\n%s", uploadOut)
	if err != nil {
		t.Fatalf("Multipart upload command failed: %v", err)
	}
	uploadedID, err := parseUploadedObjectID(uploadOut)
	if err != nil {
		t.Fatalf("Failed to parse uploaded ObjectID: %v", err)
	}
	t.Logf("Multipart upload successful. DID: %s", uploadedID)

	t.Logf("STEP 5: Executing Multipart Download and verifying bytes...")
	downloadPath := filepath.Join(t.TempDir(), "large-multipart-downloaded.txt")
	downloadOut, err := executeRootCommand(t, "--server", server.url, "download", "--did", uploadedID, "--out", downloadPath)
	t.Logf("Multipart Download Output:\n%s", downloadOut)
	if err != nil {
		t.Fatalf("Multipart download failed: %v", err)
	}
	got, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, srcData) {
		t.Fatalf("Integrity Check Failed: multipart downloaded bytes mismatch: expected %d bytes, got %d", len(srcData), len(got))
	}
	t.Logf("Multipart integrity verified.")

	t.Logf("STEP 6: Verifying hash for multipart object...")
	sumOut, err := executeRootCommand(t, "--server", server.url, "sha256sum", "--did", uploadedID)
	t.Logf("Multipart Sha256sum Output:\n%s", sumOut)
	if err != nil {
		t.Fatalf("sha256sum failed: %v", err)
	}
	expectedHash := sha256.Sum256(srcData)
	expectedSum := hex.EncodeToString(expectedHash[:])
	if !strings.Contains(sumOut, expectedSum) {
		t.Fatalf("Hash mismatch in multipart verify: expected %s, got output: %s", expectedSum, sumOut)
	}
	t.Logf("Multipart verify complete.")
}
