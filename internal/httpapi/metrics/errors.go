package metrics

import (
	"context"
	"net/http"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
)

func metricsAPIError(ctx context.Context, status int) metricsapi.APIError {
	return middleware.NewAPIError(ctx, metricsErrorCode(status), status, http.StatusText(status))
}

func metricsErrorCode(status int) errorapi.ErrorCode {
	switch status {
	case http.StatusUnauthorized:
		return errorapi.ErrorCodeAuthenticationRequired
	case http.StatusForbidden:
		return errorapi.ErrorCodeAccessDenied
	default:
		return errorapi.CodeForStatus(status)
	}
}
