package urlmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	azcontainer "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	mockServersEnvVar  = "SYFON_E2E_MOCK_SERVERS"
	fakeGCSImage       = "fsouza/fake-gcs-server:1.53.0"
	azuriteImage       = "mcr.microsoft.com/azure-storage/azurite:3.35.0"
	azuriteAccountName = "devstoreaccount1"
	azuriteAccountKey  = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

func TestMVPMockServers_FakeGCSAndAzurite(t *testing.T) {
	if strings.TrimSpace(os.Getenv(mockServersEnvVar)) != "1" {
		t.Skipf("set %s=1 to run fake-gcs-server + Azurite MVP smoke tests", mockServersEnvVar)
	}
	if testing.Short() {
		t.Skip("skipping Docker-backed MVP smoke tests in short mode")
	}

	t.Run("fake-gcs-server", func(t *testing.T) {
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
				t.Skipf("Docker is unavailable for fake-gcs-server MVP smoke test: %v", err)
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
		const bucketName = "mvp-fake-gcs-bucket"
		const objectName = "smoke/object.txt"
		payload := []byte("fake-gcs-server-mvp")
		endpointBase := fmt.Sprintf("http://%s:%s", host, port.Port())

		if err := waitForFakeGCSReady(ctx, endpointBase, 10*time.Second, 200*time.Millisecond); err != nil {
			t.Fatalf("wait for fake-gcs readiness: %v", err)
		}

		bucketBody, err := json.Marshal(map[string]string{"name": bucketName})
		if err != nil {
			t.Fatalf("marshal bucket create request: %v", err)
		}
		createBucketURL := endpointBase + "/storage/v1/b?project=syfon-mvp"
		createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createBucketURL, bytes.NewReader(bucketBody))
		if err != nil {
			t.Fatalf("build create bucket request: %v", err)
		}
		createReq.Header.Set("Content-Type", "application/json")
		createResp, err := http.DefaultClient.Do(createReq)
		if err != nil {
			t.Fatalf("create bucket: %v", err)
		}
		if createResp.StatusCode < 200 || createResp.StatusCode > 299 {
			_ = createResp.Body.Close()
			t.Fatalf("unexpected create bucket status: %s", createResp.Status)
		}
		_ = createResp.Body.Close()

		uploadURL := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s", endpointBase, bucketName, url.QueryEscape(objectName))
		putReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("build put request: %v", err)
		}
		putResp, err := http.DefaultClient.Do(putReq)
		if err != nil {
			t.Fatalf("put object: %v", err)
		}
		if putResp.StatusCode < 200 || putResp.StatusCode > 299 {
			_ = putResp.Body.Close()
			t.Fatalf("unexpected put status: %s", putResp.Status)
		}
		_ = putResp.Body.Close()

		getURL := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", endpointBase, bucketName, url.PathEscape(objectName))
		getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
		if err != nil {
			t.Fatalf("build get request: %v", err)
		}
		getResp, err := http.DefaultClient.Do(getReq)
		if err != nil {
			t.Fatalf("get object: %v", err)
		}
		t.Cleanup(func() {
			if err := getResp.Body.Close(); err != nil {
				t.Logf("warning: failed to close fake-gcs response body: %v", err)
			}
		})
		if getResp.StatusCode != http.StatusOK {
			t.Fatalf("unexpected get status: %s", getResp.Status)
		}

		buf, err := io.ReadAll(getResp.Body)
		if err != nil {
			t.Fatalf("read object body: %v", err)
		}
		if !bytes.Equal(buf, payload) {
			t.Fatalf("unexpected fake-gcs payload: got %q want %q", string(buf), string(payload))
		}
	})

	t.Run("azurite", func(t *testing.T) {

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
				t.Skipf("Docker is unavailable for Azurite MVP smoke test: %v", err)
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
		const blobName = "smoke-object.txt"
		payload := []byte("azurite-mvp")

		containerURL := fmt.Sprintf("http://%s:%s/%s/%s", host, port.Port(), azuriteAccountName, containerName)
		cred, err := azcontainer.NewSharedKeyCredential(azuriteAccountName, azuriteAccountKey)
		if err != nil {
			t.Fatalf("create Azurite shared key credential: %v", err)
		}
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

		blobClient := client.NewBlockBlobClient(blobName)
		if _, err := blobClient.UploadBuffer(ctx, payload, nil); err != nil {
			t.Fatalf("upload Azurite blob: %v", err)
		}

		downloaded := make([]byte, len(payload))
		if _, err := blobClient.DownloadBuffer(ctx, downloaded, nil); err != nil {
			t.Fatalf("download Azurite blob: %v", err)
		}
		if !bytes.Equal(downloaded, payload) {
			t.Fatalf("unexpected Azurite payload: got %q want %q", string(downloaded), string(payload))
		}
	})
}

func waitForFakeGCSReady(ctx context.Context, endpointBase string, timeout, interval time.Duration) error {
	readinessURL := endpointBase + "/storage/v1/b?project=syfon-mvp"
	return retryUntilSuccess(timeout, interval, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readinessURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("unexpected fake-gcs readiness status: %s", resp.Status)
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
		{name: "registry dns no such host should fail", err: errors.New("Get https://registry-1.docker.io/v2/: dial tcp: lookup registry-1.docker.io: no such host"), want: false},
		{name: "container create failure should fail", err: errors.New("failed to create container: image not found"), want: false},
		{name: "generic timeout should not skip", err: errors.New("context deadline exceeded"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDockerUnavailableForMockTests(tc.err); got != tc.want {
				t.Fatalf("isDockerUnavailableForMockTests(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
