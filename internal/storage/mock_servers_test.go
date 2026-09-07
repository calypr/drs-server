package storage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	azcontainer "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/azure"
	"github.com/calypr/syfon/internal/storage/gcs"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	storageMockServersEnvVar = "SYFON_E2E_MOCK_SERVERS"
	fakeGCSImage             = "fsouza/fake-gcs-server:1.53.0"
	azuriteImage             = "mcr.microsoft.com/azure-storage/azurite:3.35.0"
	azuriteAccountName       = "devstoreaccount1"
	azuriteAccountKey        = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

type credentialLookupFunc func(context.Context, string) (*buckets.Credential, error)

func (f credentialLookupFunc) GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	return f(ctx, bucket)
}

// TestStorageMockServers_FakeGCSAndAzurite keeps a provider-level smoke test
// for the two HTTP emulators that exercise the storage boundary. The test
// obtains upload and download URLs through storage.Manager and then performs
// the real provider requests against each emulator. It is intentionally kept
// separate from the command E2E tests so provider URL construction is covered
// without starting a Syfon server or a client process.
func TestStorageMockServers_FakeGCSAndAzurite(t *testing.T) {
	if strings.TrimSpace(os.Getenv(storageMockServersEnvVar)) != "1" {
		t.Skipf("set %s=1 to run fake-gcs-server + Azurite storage smoke tests", storageMockServersEnvVar)
	}
	if testing.Short() {
		t.Skip("skipping Docker-backed storage smoke tests in short mode")
	}

	t.Run("fake-gcs-server", testFakeGCSStorageProvider)
	t.Run("azurite", testAzuriteStorageProvider)
}

func testFakeGCSStorageProvider(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        fakeGCSImage,
			ExposedPorts: []string{"4443/tcp"},
			Cmd:          []string{"-scheme", "http", "-port", "4443"},
			WaitingFor:   wait.ForListeningPort("4443/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if isDockerUnavailableForMockTests(err) {
			t.Skipf("Docker is unavailable for fake-gcs-server storage smoke test: %v", err)
		}
		t.Fatalf("start fake-gcs-server container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = container.Terminate(cleanupCtx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("resolve fake-gcs-server host: %v", err)
	}
	port, err := container.MappedPort(ctx, "4443/tcp")
	if err != nil {
		t.Fatalf("resolve fake-gcs-server mapped port: %v", err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())
	if err := waitForFakeGCSReady(ctx, endpoint, 10*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("wait for fake-gcs readiness: %v", err)
	}

	const bucket = "mvp-fake-gcs-bucket"
	if err := createFakeGCSBucket(ctx, endpoint, bucket); err != nil {
		t.Fatalf("create fake-gcs bucket: %v", err)
	}

	lookup := credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return &buckets.Credential{Bucket: bucket, Provider: "gcs", Endpoint: endpoint}, nil
	})
	manager, err := storage.NewManager(lookup, gcs.New(lookup))
	if err != nil {
		t.Fatalf("create GCS storage manager: %v", err)
	}

	const object = "smoke/object.txt"
	payload := []byte("fake-gcs-server-storage-mvp")
	upload, err := manager.Access(ctx, storage.AccessRequest{
		Target:  storage.AccessTarget{Location: "s3://" + bucket + "/" + object},
		Options: storage.AccessOptions{Method: http.MethodPut},
	})
	if err != nil {
		t.Fatalf("sign fake-gcs upload URL: %v", err)
	}
	if err := uploadObject(ctx, http.MethodPost, upload.Location, payload, nil); err != nil {
		t.Fatalf("upload fake-gcs object: %v", err)
	}

	download, err := manager.Access(ctx, storage.AccessRequest{
		Target: storage.AccessTarget{Location: "s3://" + bucket + "/" + object},
	})
	if err != nil {
		t.Fatalf("sign fake-gcs download URL: %v", err)
	}
	got, err := downloadObject(ctx, download.Location)
	if err != nil {
		t.Fatalf("download fake-gcs object: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("unexpected fake-gcs payload: got %q want %q", string(got), string(payload))
	}
}

func testAzuriteStorageProvider(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        azuriteImage,
			ExposedPorts: []string{"10000/tcp"},
			Cmd:          []string{"azurite-blob", "--blobHost", "0.0.0.0", "--blobPort", "10000", "--skipApiVersionCheck"},
			WaitingFor:   wait.ForListeningPort("10000/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if isDockerUnavailableForMockTests(err) {
			t.Skipf("Docker is unavailable for Azurite storage smoke test: %v", err)
		}
		t.Fatalf("start Azurite container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = container.Terminate(cleanupCtx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("resolve Azurite host: %v", err)
	}
	port, err := container.MappedPort(ctx, "10000/tcp")
	if err != nil {
		t.Fatalf("resolve Azurite mapped port: %v", err)
	}

	const containerName = "mvpazuritecontainer"
	const object = "smoke-object.txt"
	endpoint := fmt.Sprintf("http://%s:%s/%s", host, port.Port(), azuriteAccountName)
	cred, err := azcontainer.NewSharedKeyCredential(azuriteAccountName, azuriteAccountKey)
	if err != nil {
		t.Fatalf("create Azurite shared key credential: %v", err)
	}
	containerURL := strings.TrimRight(endpoint, "/") + "/" + containerName
	client, err := azcontainer.NewClientWithSharedKeyCredential(containerURL, cred, nil)
	if err != nil {
		t.Fatalf("create Azurite container client: %v", err)
	}
	if err := waitForAzuriteReady(ctx, client, 10*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("wait for Azurite readiness: %v", err)
	}
	if _, err := client.Create(ctx, nil); err != nil {
		t.Fatalf("create Azurite blob container: %v", err)
	}

	lookup := credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return &buckets.Credential{
			Bucket:    containerName,
			Provider:  "azure",
			AccessKey: azuriteAccountName,
			SecretKey: azuriteAccountKey,
			Endpoint:  endpoint,
		}, nil
	})
	manager, err := storage.NewManager(lookup, azure.New(lookup))
	if err != nil {
		t.Fatalf("create Azure storage manager: %v", err)
	}

	payload := []byte("azurite-storage-mvp")
	upload, err := manager.Access(ctx, storage.AccessRequest{
		Target:  storage.AccessTarget{Location: "s3://" + containerName + "/" + object},
		Options: storage.AccessOptions{Method: http.MethodPut},
	})
	if err != nil {
		t.Fatalf("sign Azurite upload URL: %v", err)
	}
	if err := uploadObject(ctx, http.MethodPut, upload.Location, payload, http.Header{"x-ms-blob-type": []string{"BlockBlob"}}); err != nil {
		t.Fatalf("upload Azurite object: %v", err)
	}

	download, err := manager.Access(ctx, storage.AccessRequest{
		Target: storage.AccessTarget{Location: "s3://" + containerName + "/" + object},
	})
	if err != nil {
		t.Fatalf("sign Azurite download URL: %v", err)
	}
	got, err := downloadObject(ctx, download.Location)
	if err != nil {
		t.Fatalf("download Azurite object: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("unexpected Azurite payload: got %q want %q", string(got), string(payload))
	}
}

func createFakeGCSBucket(ctx context.Context, endpoint, bucket string) error {
	body, err := json.Marshal(map[string]string{"name": bucket})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/storage/v1/b?project=syfon-mvp", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		responseBody, _ := io.ReadAll(response.Body)
		return fmt.Errorf("create bucket failed: status=%s body=%s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func uploadObject(ctx context.Context, method, location string, payload []byte, headers http.Header) error {
	request, err := http.NewRequestWithContext(ctx, method, location, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		responseBody, _ := io.ReadAll(response.Body)
		return fmt.Errorf("upload failed: status=%s body=%s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func downloadObject(ctx context.Context, location string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("download failed: status=%s body=%s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return io.ReadAll(response.Body)
}

func waitForFakeGCSReady(ctx context.Context, endpoint string, timeout, interval time.Duration) error {
	readinessURL := strings.TrimRight(endpoint, "/") + "/storage/v1/b?project=syfon-mvp"
	return retryUntilSuccess(timeout, interval, func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, readinessURL, nil)
		if err != nil {
			return err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode > 299 {
			return fmt.Errorf("unexpected fake-gcs readiness status: %s", response.Status)
		}
		return nil
	})
}

func waitForAzuriteReady(ctx context.Context, client *azcontainer.Client, timeout, interval time.Duration) error {
	return retryUntilSuccess(timeout, interval, func() error {
		_, err := client.GetProperties(ctx, nil)
		if err == nil {
			return nil
		}
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "containernotfound") || strings.Contains(lower, "resource not found") {
			return nil
		}
		return err
	})
}

func retryUntilSuccess(timeout, interval time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(interval)
	}
}

func isDockerUnavailableForMockTests(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "docker daemon") ||
		strings.Contains(lower, "cannot connect") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "failed to create docker provider")
}

func TestIsDockerUnavailableForMockTests(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "docker daemon unavailable", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"), want: true},
		{name: "provider creation failed", err: errors.New("failed to create Docker provider"), want: true},
		{name: "registry DNS failure should fail", err: errors.New("Get https://registry-1.docker.io/v2/: dial tcp: lookup registry-1.docker.io: no such host"), want: false},
		{name: "container create failure should fail", err: errors.New("failed to create container: image not found"), want: false},
		{name: "generic timeout should not skip", err: errors.New("context deadline exceeded"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDockerUnavailableForMockTests(test.err); got != test.want {
				t.Fatalf("isDockerUnavailableForMockTests(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

var _ storage.CredentialLookup = credentialLookupFunc(nil)
