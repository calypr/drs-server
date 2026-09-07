package lfs

import "context"

// baseURLKey is intentionally private to this HTTP adapter.  A request's
// reverse-proxy prefix is protocol state and must not leak into transfers or
// the object domain.
type baseURLKey struct{}

// WithBaseURL attaches the Fiber request base URL to a request context.
func WithBaseURL(ctx context.Context, baseURL string) context.Context {
	return context.WithValue(ctx, baseURLKey{}, baseURL)
}

// GetBaseURL returns the request base URL previously attached by the route
// adapter.
func GetBaseURL(ctx context.Context) string {
	value, _ := ctx.Value(baseURLKey{}).(string)
	return value
}
