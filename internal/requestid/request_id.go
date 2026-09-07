package requestid

import "context"

type requestIDKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func GetRequestID(ctx context.Context) string {
	v := ctx.Value(requestIDKey{})
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
