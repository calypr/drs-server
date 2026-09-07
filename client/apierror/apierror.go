package apierror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
	ErrInvalidInput = errors.New("invalid input")
	ErrRateLimited  = errors.New("rate limited")
	ErrUnavailable  = errors.New("service unavailable")
)

type Code string

const (
	CodeNotFound      Code = "not_found"
	CodeUnauthorized  Code = "unauthorized"
	CodeForbidden     Code = "forbidden"
	CodeConflict      Code = "conflict"
	CodeInvalidInput  Code = "invalid_input"
	CodeRateLimited   Code = "rate_limited"
	CodeUnavailable   Code = "unavailable"
	CodeInternal      Code = "internal_error"
	CodeRequestFailed Code = "request_failed"
)

type APIError struct {
	Code      Code
	Status    int
	Message   string
	RequestID string
	Method    string
	URL       string
	Headers   http.Header
	Body      string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = strings.TrimSpace(e.Body)
	}
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if message == "" {
		message = "request failed"
	}
	return fmt.Sprintf("%s %s: status %d body=%s", e.Method, e.URL, e.Status, message)
}

func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return sentinelFor(e.Status, e.Code)
}

func (e *APIError) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	return errors.Is(e.Unwrap(), target)
}

func FromResponse(resp *http.Response, body []byte) *APIError {
	err := &APIError{
		Status: httpStatus(resp),
		Body:   strings.TrimSpace(string(body)),
	}
	if resp != nil {
		err.Headers = resp.Header.Clone()
		if resp.Request != nil {
			err.Method = resp.Request.Method
			if resp.Request.URL != nil {
				err.URL = resp.Request.URL.String()
			}
		}
		err.RequestID = firstNonEmpty(resp.Header.Get("X-Request-ID"), resp.Header.Get("X-Request-Id"))
	}
	decodePayload(err, body)
	if err.Code == "" {
		err.Code = codeForStatus(err.Status)
	}
	if err.Message == "" {
		err.Message = err.Body
	}
	return err
}

func decodePayload(err *APIError, body []byte) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return
	}
	decodeObject(err, payload)
}

func decodeObject(err *APIError, payload map[string]any) {
	if payload == nil {
		return
	}
	err.Code = Code(firstNonEmpty(string(err.Code), valueString(payload["code"]), valueString(payload["error_code"]), valueString(payload["type"])))
	err.Message = firstNonEmpty(err.Message, valueString(payload["message"]), valueString(payload["msg"]))
	err.RequestID = firstNonEmpty(err.RequestID, valueString(payload["request_id"]), valueString(payload["requestId"]))
	if nested, ok := payload["error"].(map[string]any); ok {
		decodeObject(err, nested)
	} else if nested := valueString(payload["error"]); nested != "" {
		err.Message = firstNonEmpty(err.Message, nested)
	}
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func sentinelFor(status int, code Code) error {
	switch status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusConflict:
		return ErrConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return ErrInvalidInput
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrUnavailable
	}

	switch normalizeCode(code) {
	case "notfound", "resourcenotfound":
		return ErrNotFound
	case "unauthorized", "authenticationrequired":
		return ErrUnauthorized
	case "forbidden", "permissiondenied":
		return ErrForbidden
	case "conflict":
		return ErrConflict
	case "invalidinput", "badrequest", "unprocessableentity":
		return ErrInvalidInput
	case "ratelimited", "toomanyrequests":
		return ErrRateLimited
	case "unavailable", "serviceunavailable", "badgateway", "gatewaytimeout":
		return ErrUnavailable
	}
	return nil
}

func normalizeCode(code Code) string {
	normalized := strings.ToLower(strings.TrimSpace(string(code)))
	normalized = strings.NewReplacer("_", "", "-", "", " ", "").Replace(normalized)
	if n, err := strconv.Atoi(normalized); err == nil {
		mapped := codeForStatus(n)
		if mapped != Code(normalized) {
			return normalizeCode(mapped)
		}
	}
	return normalized
}

func codeForStatus(status int) Code {
	switch status {
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusConflict:
		return CodeConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return CodeInvalidInput
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	default:
		return Code(strconv.Itoa(status))
	}
}

func httpStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
