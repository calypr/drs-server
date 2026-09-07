package s3

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	defaultListPageMaxAttempts      = 12
	defaultExactProbeMaxAttempts    = 3
	defaultListPageInitialBackoff   = 500 * time.Millisecond
	defaultListPageMaxBackoff       = 10 * time.Second
	defaultInventoryTerminalReplays = 3
	envListPageMaxAttempts          = "SYFON_S3_LIST_PAGE_MAX_ATTEMPTS"
	envExactProbeMaxAttempts        = "SYFON_S3_EXACT_PROBE_MAX_ATTEMPTS"
	envListPageInitialBackoff       = "SYFON_S3_LIST_PAGE_INITIAL_BACKOFF_MS"
	envListPageMaxBackoff           = "SYFON_S3_LIST_PAGE_MAX_BACKOFF_MS"
	envInventoryTerminalReplays     = "SYFON_S3_INVENTORY_TERMINAL_REPLAY_ATTEMPTS"
)

type listPageRetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func listPageRetryPolicyFromEnv() listPageRetryPolicy {
	return listPageRetryPolicy{
		MaxAttempts:    intEnvOrDefault(envListPageMaxAttempts, defaultListPageMaxAttempts, 1),
		InitialBackoff: millisEnvOrDefault(envListPageInitialBackoff, defaultListPageInitialBackoff, 0),
		MaxBackoff:     millisEnvOrDefault(envListPageMaxBackoff, defaultListPageMaxBackoff, 0),
	}
}

func intEnvOrDefault(name string, fallback, minimum int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return fallback
	}
	return value
}

func millisEnvOrDefault(name string, fallback time.Duration, minimumMillis int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimumMillis {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func (policy listPageRetryPolicy) backoff(attempt int) time.Duration {
	backoff := policy.InitialBackoff
	for index := 1; index < attempt; index++ {
		backoff *= 2
		if backoff >= policy.MaxBackoff {
			backoff = policy.MaxBackoff
			break
		}
	}
	if backoff <= 0 {
		return 0
	}
	jitterMax := backoff / 4
	if jitterMax <= 0 {
		return backoff
	}
	return backoff + time.Duration(rand.Int63n(int64(jitterMax)+1))
}

var sleepListPageRetry = func(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableListPageError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
		case "internalerror", "slowdown", "serviceunavailable", "requesttimeout", "requesttimeoutexception", "toomanyrequests", "throttling", "throttlingexception", "requestlimitexceeded":
			return true
		default:
			return false
		}
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "eof") || strings.Contains(message, "connection reset") || strings.Contains(message, "timeout") ||
		strings.Contains(message, "malformed truncated list page") || strings.Contains(message, "empty list page")
}

func continuationTokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "start"
	}
	sum := sha1.Sum([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

// listPageFingerprint intentionally excludes NextContinuationToken. This is
// the existing terminal replay identity and remains a compatibility quirk.
func listPageFingerprint(page *awss3.ListObjectsV2Output) string {
	if page == nil {
		return "nil"
	}
	objects := append([]types.Object(nil), page.Contents...)
	// The old fingerprint sorted only object fields, not the token.
	for i := 0; i < len(objects); i++ {
		for j := i + 1; j < len(objects); j++ {
			if stringValue(objects[j].Key) < stringValue(objects[i].Key) {
				objects[i], objects[j] = objects[j], objects[i]
			}
		}
	}
	var builder strings.Builder
	if page.IsTruncated != nil {
		builder.WriteString("truncated=")
		if *page.IsTruncated {
			builder.WriteString("true\n")
		} else {
			builder.WriteString("false\n")
		}
	} else {
		builder.WriteString("truncated=false\n")
	}
	for _, object := range objects {
		lastModified := ""
		if object.LastModified != nil {
			lastModified = object.LastModified.UTC().Format(time.RFC3339Nano)
		}
		builder.WriteString(stringValue(object.Key))
		builder.WriteByte(0)
		builder.WriteString(strconv.FormatInt(int64Value(object.Size), 10))
		builder.WriteByte(0)
		builder.WriteString(stringValue(object.ETag))
		builder.WriteByte(0)
		builder.WriteString(lastModified)
		builder.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])[:16]
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
