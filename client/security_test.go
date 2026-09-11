package client

import (
	"net/http"
	"testing"
	"time"
)

// Test INFO-3 fix: Client has reasonable timeout
func TestDefaultConfig_HasTimeout(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.HTTPClient == nil {
		t.Fatal("HTTPClient is nil")
	}

	// SECURITY FIX INFO-3: Should have a reasonable timeout
	if cfg.HTTPClient.Timeout == 0 {
		t.Errorf("HTTPClient.Timeout = 0, should have a reasonable timeout")
	}

	// Should be 10 minutes
	if cfg.HTTPClient.Timeout != 10*time.Minute {
		t.Errorf("HTTPClient.Timeout = %v, want 10m", cfg.HTTPClient.Timeout)
	}
}

// Test that New() also gets a client with timeout
func TestNew_ClientHasTimeout(t *testing.T) {
	// Using New without a custom config should give a client with timeout
	syClient, err := New("http://localhost:8080")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if syClient.httpClient == nil {
		t.Fatal("HTTP client is nil")
	}
}

// Test that custom config without timeout is rejected
func TestNewClient_WithCustomConfig(t *testing.T) {
	customClient := &http.Client{Timeout: 30 * time.Second}
	cfg := &Config{
		Address:    "http://localhost:8080",
		HTTPClient: customClient,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.HTTPClient() == nil {
		t.Fatal("HTTP client is nil")
	}
	if client.HTTPClient().Timeout != 30*time.Second {
		t.Errorf("HTTPClient().Timeout = %v, want 30s", client.HTTPClient().Timeout)
	}
}

// Test that nil config still gets timeout
func TestNewClient_WithNilConfig(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient(nil) error = %v", err)
	}

	if client.HTTPClient() == nil {
		t.Fatal("HTTP client is nil")
	}
	if client.HTTPClient().Timeout == 0 {
		t.Errorf("HTTPClient().Timeout = 0, should have a reasonable timeout")
	}
}
