package services

import (
	"context"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/request"
)

type HealthService struct {
	requestor request.Requester
}

func NewHealthService(r request.Requester) *HealthService {
	return &HealthService{requestor: r}
}

func (h *HealthService) Ping(ctx context.Context) error {
	return h.requestor.Do(ctx, "GET", common.HealthzEndpoint, nil, nil)
}

// --- HealthService ---
