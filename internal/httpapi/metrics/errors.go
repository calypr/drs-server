package metrics

import (
	"context"
	"net/http"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/httpapi/response"
)

func metricsAPIError(ctx context.Context, status int) metricsapi.APIError {
	return response.NewAPIError(ctx, metricsErrorCode(status), status, http.StatusText(status))
}

func metricsErrorCode(status int) errorapi.ErrorCode {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return errorapi.ErrorCodeInvalidInput
	case http.StatusUnauthorized:
		return errorapi.ErrorCodeAuthenticationRequired
	case http.StatusForbidden:
		return errorapi.ErrorCodeAccessDenied
	case http.StatusNotFound:
		return errorapi.ErrorCodeNotFound
	case http.StatusConflict:
		return errorapi.ErrorCodeConflict
	case http.StatusTooManyRequests:
		return errorapi.ErrorCodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return errorapi.ErrorCodeUnavailable
	case http.StatusInternalServerError:
		return errorapi.ErrorCodeInternalError
	default:
		return errorapi.ErrorCodeRequestFailed
	}
}
