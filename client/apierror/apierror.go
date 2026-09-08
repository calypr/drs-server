package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
)

type APIError struct {
	Code      errorapi.ErrorCode
	Category  errorapi.ErrorCategory
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
	if message == "" && e.Status < http.StatusInternalServerError {
		message = strings.TrimSpace(e.Body)
	}
	if message == "" {
		message = http.StatusText(e.Status)
		if message == "" && e.Status >= http.StatusInternalServerError {
			message = http.StatusText(http.StatusInternalServerError)
		}
	}
	if message == "" {
		message = "request failed"
	}
	return fmt.Sprintf("%s %s: status %d body=%s", e.Method, e.URL, e.Status, message)
}

func (e *APIError) ErrorCode() errorapi.ErrorCode {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *APIError) ErrorCategory() errorapi.ErrorCategory {
	if e == nil {
		return ""
	}
	return e.Category
}

func (e *APIError) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	targetCode, hasCode := errorapi.CodeOf(target)
	if hasCode && e.Code == targetCode {
		return true
	}
	targetCategory, hasCategory := errorapi.CategoryOf(target)
	if !hasCategory || e.Category != targetCategory {
		return false
	}
	return !hasCode || errorapi.IsBroadCode(targetCode)
}

func FromResponse(resp *http.Response, body []byte) *APIError {
	err := &APIError{
		Status: httpStatus(resp),
		Body:   string(body),
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
		err.Code = errorapi.CodeForStatus(err.Status)
	}
	if category, ok := errorapi.CategoryForCode(err.Code); ok {
		err.Category = category
	} else if err.Category == "" {
		err.Category = errorapi.CategoryForStatus(err.Status)
	}
	if err.Message == "" {
		err.Message = strings.TrimSpace(err.Body)
	}
	if err.Message == "" {
		err.Message = http.StatusText(err.Status)
		if err.Message == "" && err.Status >= http.StatusInternalServerError {
			err.Message = http.StatusText(http.StatusInternalServerError)
		}
	}
	if err.Message == "" {
		err.Message = "request failed"
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
	err.Code = firstErrorCode(payload)
	decodeObject(err, payload)
}

func decodeObject(err *APIError, payload map[string]any) {
	if payload == nil {
		return
	}
	err.Category = errorapi.ErrorCategory(firstNonEmpty(string(err.Category), valueString(payload["category"]), valueString(payload["error_category"])))
	err.Message = firstNonEmpty(err.Message, valueString(payload["message"]), valueString(payload["msg"]))
	err.RequestID = firstNonEmpty(err.RequestID, valueString(payload["request_id"]), valueString(payload["requestId"]))
	if nested, ok := payload["error"].(map[string]any); ok {
		decodeObject(err, nested)
	} else if nested := valueString(payload["error"]); nested != "" {
		err.Message = firstNonEmpty(err.Message, nested)
	}
}

func firstErrorCode(payload map[string]any) errorapi.ErrorCode {
	applicationCode, numericCode := scanErrorCodes(payload)
	if applicationCode != "" {
		return applicationCode
	}
	return numericCode
}

func scanErrorCodes(payload map[string]any) (errorapi.ErrorCode, errorapi.ErrorCode) {
	if payload == nil {
		return "", ""
	}

	var numericCode errorapi.ErrorCode
	for _, key := range []string{"error_code", "code", "type"} {
		raw := valueString(payload[key])
		if raw == "" {
			continue
		}
		if status, err := strconv.Atoi(raw); err == nil {
			if numericCode == "" {
				numericCode = errorapi.CodeForStatus(status)
			}
			continue
		}
		return errorapi.ErrorCode(raw), numericCode
	}

	if nested, ok := payload["error"].(map[string]any); ok {
		nestedApplicationCode, nestedNumericCode := scanErrorCodes(nested)
		if nestedApplicationCode != "" {
			return nestedApplicationCode, numericCode
		}
		if numericCode == "" {
			numericCode = nestedNumericCode
		}
	}
	return "", numericCode
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		number := strconv.FormatFloat(typed, 'f', -1, 64)
		if _, err := strconv.ParseInt(number, 10, 64); err != nil {
			return ""
		}
		return number
	case json.Number:
		return typed.String()
	default:
		return ""
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
