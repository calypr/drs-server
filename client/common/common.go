package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	B  int64 = 1
	KB int64 = 1024 * B
	MB int64 = 1024 * KB
	GB int64 = 1024 * MB
	TB int64 = 1024 * GB

	DataAccessTokenEndpoint = "/user/credentials/api/access_token"
	DataTimeout             = 5 * time.Minute
	HeaderContentType       = "Content-Type"
	MIMEApplicationJSON     = "application/json"
	FileSizeLimit           = 5 * GB
	MaxRetryCount           = 5
	MaxWaitTime             = 300
	MaxConcurrentUploads    = 10
	OnProgressThreshold     = 1 * MB
	HealthzEndpoint         = "/healthz"
)

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
