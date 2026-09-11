package services

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestHealthService_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Path != "/healthz" {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
		})}
		h := NewHealthService("https://example.test", client)
		if err := h.Ping(context.Background()); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("health check failed")
		})}
		h := NewHealthService("https://example.test", client)
		if err := h.Ping(context.Background()); err == nil {
			t.Error("expected error, got nil")
		}
	})
}
