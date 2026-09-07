package common

import "context"

type ProgressEvent struct {
	Event          string         `json:"event"`
	Oid            string         `json:"oid"`
	BytesSoFar     int64          `json:"bytesSoFar"`
	BytesSinceLast int64          `json:"bytesSinceLast"`
	Message        string         `json:"message,omitempty"`
	Level          string         `json:"level,omitempty"`
	Attrs          map[string]any `json:"attrs,omitempty"`
}

type ProgressCallback func(ProgressEvent) error

type contextKey string

const (
	progressKey contextKey = "progressCallback"
	oidKey      contextKey = "activeOid"
)

func WithProgress(ctx context.Context, cb ProgressCallback) context.Context {
	return context.WithValue(ctx, progressKey, cb)
}

func GetProgress(ctx context.Context) ProgressCallback {
	if cb, ok := ctx.Value(progressKey).(ProgressCallback); ok {
		return cb
	}
	return nil
}

func WithOid(ctx context.Context, oid string) context.Context {
	return context.WithValue(ctx, oidKey, oid)
}

func GetOid(ctx context.Context) string {
	if oid, ok := ctx.Value(oidKey).(string); ok {
		return oid
	}
	return ""
}
