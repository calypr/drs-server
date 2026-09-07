package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeReturnsConfigLoadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	previous := configFile
	configFile = path
	t.Cleanup(func() { configFile = previous })

	err := Cmd.RunE(Cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("expected config load error, got %v", err)
	}
}
