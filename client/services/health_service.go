package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/request"
)

type HealthService struct {
	baseURL    string
	httpClient request.HTTPDoer
}

func NewHealthService(baseURL string, client request.HTTPDoer) *HealthService {
	return &HealthService{baseURL: strings.TrimRight(baseURL, "/"), httpClient: client}
}

func (h *HealthService) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+common.HealthzEndpoint, nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("health check request returned no response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read health check response: %w", err)
	}
	return apierror.FromResponse(resp, body)
}
