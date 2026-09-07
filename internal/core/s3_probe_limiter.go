package core

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/requestmeta"
)

const (
	defaultS3ProbeConcurrency = 8
	envS3ProbeConcurrency     = "SYFON_S3_PROBE_CONCURRENCY"
)

var s3ProbeLimiterContextKey contextKey = "s3ProbeLimiter"

type s3ProbeLimiter struct {
	permits chan struct{}
}

func newS3ProbeLimiter(limit int) *s3ProbeLimiter {
	if limit < 1 {
		limit = defaultS3ProbeConcurrency
	}
	return &s3ProbeLimiter{permits: make(chan struct{}, limit)}
}

func newS3ProbeLimiterFromEnv() *s3ProbeLimiter {
	return newS3ProbeLimiter(s3ProbeConcurrencyFromEnv())
}

func s3ProbeConcurrencyFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(envS3ProbeConcurrency))
	if raw == "" {
		return defaultS3ProbeConcurrency
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return defaultS3ProbeConcurrency
	}
	return limit
}

func withS3ProbeLimiter(ctx context.Context, limiter *s3ProbeLimiter) context.Context {
	if limiter == nil || ctx.Value(s3ProbeLimiterContextKey) != nil {
		return ctx
	}
	return context.WithValue(ctx, s3ProbeLimiterContextKey, limiter)
}

func acquireS3Probe(ctx context.Context, operation, bucket, key string) (func(), error) {
	limiter, _ := ctx.Value(s3ProbeLimiterContextKey).(*s3ProbeLimiter)
	if limiter == nil {
		return func() {}, nil
	}
	started := time.Now()
	select {
	case limiter.permits <- struct{}{}:
		waited := time.Since(started)
		if waited >= 100*time.Millisecond {
			log.Printf("INFO: syfon_s3_probe_limiter_wait request_id=%s operation=%s bucket=%s key=%q wait_ms=%d", requestmeta.GetRequestID(ctx), operation, bucket, key, waited.Milliseconds())
		}
		return func() { <-limiter.permits }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
