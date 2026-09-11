package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func ToJSONReader(payload any) (io.Reader, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, fmt.Errorf("failed to encode JSON payload: %w", err)
	}
	return &buf, nil
}

func ParseRootPath(filePath string) (string, error) {
	if filePath != "" && filePath[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return homeDir + filePath[1:], nil
	}
	return filePath, nil
}

func GetAbsolutePath(filePath string) (string, error) {
	fullFilePath, err := ParseRootPath(filePath)
	if err != nil {
		return "", err
	}
	return filepath.Abs(fullFilePath)
}

func IsCloudPresignedURL(raw string) bool {
	return strings.Contains(raw, "X-Amz-Signature") ||
		strings.Contains(raw, "X-Goog-Signature") ||
		strings.Contains(raw, "Signature=") ||
		strings.Contains(raw, "AWSAccessKeyId=") ||
		strings.Contains(raw, "Expires=")
}

func FormatSize(size int64) string {
	unitSize := B
	switch {
	case size >= TB:
		unitSize = TB
	case size >= GB:
		unitSize = GB
	case size >= MB:
		unitSize = MB
	case size >= KB:
		unitSize = KB
	}
	units := map[int64]string{B: "B", KB: "KB", MB: "MB", GB: "GB", TB: "TB"}
	return fmt.Sprintf("%.1f%s", float64(size)/float64(unitSize), units[unitSize])
}

type FileMetadata struct {
	Authorizations map[string][]string `json:"authorizations,omitempty"`
	Aliases        []string            `json:"aliases"`
	Metadata       map[string]any      `json:"metadata"`
}

func ResponseBodyError(resp *http.Response, prefix string) error {
	if resp == nil {
		return fmt.Errorf("%s: nil response", prefix)
	}

	const maxBodyPreview = 4 << 10
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyPreview))
	if err != nil {
		return fmt.Errorf("%s: status %d body-read-error=%v", prefix, resp.StatusCode, err)
	}
	body := strings.TrimSpace(string(bodyBytes))
	if body == "" {
		return fmt.Errorf("%s: status %d", prefix, resp.StatusCode)
	}
	return fmt.Errorf("%s: status %d body=%s", prefix, resp.StatusCode, body)
}
