package services

import (
	"encoding/json"
	"fmt"
	"strings"
)

func apiResponseError(prefix string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg != "" {
		var payload struct {
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			switch {
			case payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "":
				msg = strings.TrimSpace(payload.Error.Message)
			case strings.TrimSpace(payload.Message) != "":
				msg = strings.TrimSpace(payload.Message)
			}
		}
		return fmt.Errorf("%s: %d: %s", prefix, status, msg)
	}
	return fmt.Errorf("%s: %d", prefix, status)
}

type responseStatusError struct{ status int }

func (e *responseStatusError) Error() string { return fmt.Sprintf("unexpected response: %d", e.status) }
