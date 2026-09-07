package metrics

import (
	"context"
	"net/http"

	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/httpapi/response"
)

func metricsAPIError(ctx context.Context, status int) metricsapi.APIError {
	payload := response.NewAPIError(ctx, metricsErrorCode(status), status, http.StatusText(status))
	msg := payload.Msg
	out := metricsapi.APIError{
		Code:    metricsapi.APIErrorCode(payload.Code),
		Status:  payload.Status,
		Message: payload.Message,
		Msg:     &msg,
	}
	if payload.RequestID != "" {
		out.RequestId = &payload.RequestID
	}
	return out
}

func metricsErrorCode(status int) faults.Code {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return faults.CodeInvalidInput
	case http.StatusUnauthorized:
		return faults.CodeUnauthorized
	case http.StatusForbidden:
		return faults.CodeForbidden
	case http.StatusNotFound:
		return faults.CodeNotFound
	case http.StatusConflict:
		return faults.CodeConflict
	case http.StatusTooManyRequests:
		return faults.CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return faults.CodeUnavailable
	case http.StatusInternalServerError:
		return faults.CodeInternal
	default:
		return faults.CodeRequestFailed
	}
}
