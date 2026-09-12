package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
)

const (
	dockerE2EEnvVar          = "SYFON_E2E_DOCKER"
	dockerE2ECredentialKey   = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	dockerE2EServerReadyWait = 20 * time.Second
	dockerE2EBasicUser       = "drs-user"
	dockerE2EBasicPass       = "drs-pass"
)

type syfonServerProcess struct {
	url       string
	cmd       *exec.Cmd
	waitErrCh <-chan error
	stdout    *synchronizedBuffer
	stderr    *synchronizedBuffer
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func startSyfonServerProcessWithConfigPath(t *testing.T, configPath string, extraEnv map[string]string) *syfonServerProcess {
	t.Helper()
	return startSyfonServerProcessWithBinary(t, buildSyfonBinary(t, findRepoRoot(t)), configPath, extraEnv)
}

func startSyfonServerProcessWithBinary(t *testing.T, binaryPath, configPath string, extraEnv map[string]string) *syfonServerProcess {
	t.Helper()

	rootDir := findRepoRoot(t)
	serverPort := extractPortFromConfig(t, configPath)
	serverURL := fmt.Sprintf("http://%s:%s@127.0.0.1:%d", dockerE2EBasicUser, dockerE2EBasicPass, serverPort)
	readyURL := fmt.Sprintf("http://127.0.0.1:%d", serverPort)

	cmd := exec.Command(binaryPath, "serve", "--config", configPath)
	cmd.Dir = rootDir
	cmd.Env = append(os.Environ(), credentialcipher.CredentialMasterKeyEnv+"="+dockerE2ECredentialKey)
	for key, val := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+val)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutBuf := &synchronizedBuffer{}
	stderrBuf := &synchronizedBuffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	cmd.Env = cmd.Environ()

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
	if server == nil || server.cmd == nil || server.cmd.Process == nil || server.cmd.ProcessState != nil {
		return
	}

	_ = syscall.Kill(-server.cmd.Process.Pid, syscall.SIGINT)
	select {
	case err := <-server.waitErrCh:
		if err != nil {
			logServerProcessOutput(t, server.url, server.stdout, server.stderr)
			t.Fatalf("server process exited with error after shutdown signal: %v", err)
		}
		return
	case <-time.After(5 * time.Second):
	}

	_ = server.cmd.Process.Kill()
	select {
	case err := <-server.waitErrCh:
		if err == nil {
			return
		}
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

func buildSyfonBinary(t *testing.T, rootDir string) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "syfon-e2e")
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

func extractPortFromConfig(t *testing.T, configPath string) int {
	t.Helper()

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file %s: %v", configPath, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "port:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "port:"))
			port, convErr := strconv.Atoi(value)
			if convErr != nil {
				t.Fatalf("parse port from %s: %v", configPath, convErr)
			}
			return port
		}
	}
	t.Fatalf("port is missing in %s", configPath)
	return 0
}

func logServerProcessOutput(t *testing.T, serverURL string, stdoutBuf, stderrBuf *synchronizedBuffer) {
	t.Helper()
	if stdoutBuf != nil {
		t.Logf("server %s stdout:\n%s", serverURL, stdoutBuf.String())
	}
	if stderrBuf != nil {
		t.Logf("server %s stderr:\n%s", serverURL, stderrBuf.String())
	}
}
