package s3

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/requestid"
)

const (
	defaultProbeConcurrency = 8
	envProbeConcurrency     = "SYFON_S3_PROBE_CONCURRENCY"
)

type probeLimiter struct {
	permits chan struct{}
}

func newProbeLimiter(limit int) *probeLimiter {
	if limit < 1 {
		limit = defaultProbeConcurrency
	}
	return &probeLimiter{permits: make(chan struct{}, limit)}
}

func newProbeLimiterFromEnv() *probeLimiter {
	raw := strings.TrimSpace(os.Getenv(envProbeConcurrency))
	limit, err := strconv.Atoi(raw)
	if raw == "" || err != nil || limit < 1 {
		limit = defaultProbeConcurrency
	}
	return newProbeLimiter(limit)
}

func (s *backend) acquireProbe(ctx context.Context, operation, bucket, key string) (func(), error) {
	if s.limiter == nil {
		return func() {}, nil
	}
	started := time.Now()
	select {
	case s.limiter.permits <- struct{}{}:
		waited := time.Since(started)
		if waited >= 100*time.Millisecond {
			log.Printf("INFO: syfon_s3_probe_limiter_wait request_id=%s operation=%s bucket=%s key=%q wait_ms=%d", requestid.GetRequestID(ctx), operation, bucket, key, waited.Milliseconds())
		}
		return func() { <-s.limiter.permits }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
