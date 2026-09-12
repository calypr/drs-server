package common

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return false
	}

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	nonEmpty := func(key string) bool {
		for _, value := range query[key] {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
		return false
	}

	// AWS and GCS V4 URLs carry provider-specific signature parameters.
	if nonEmpty("X-Amz-Signature") || nonEmpty("X-Goog-Signature") {
		return true
	}

	// Legacy S3/GCS URLs need a signature's companion expiry or access key.
	if nonEmpty("Signature") && nonEmpty("Expires") {
		return true
	}
	if (nonEmpty("AWSAccessKeyId") || nonEmpty("GoogleAccessId")) && (nonEmpty("Signature") || nonEmpty("Expires")) {
		return true
	}

	// Azure SAS emits sig together with either its version or expiry field.
	return nonEmpty("sig") && (nonEmpty("sv") || nonEmpty("se"))
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
