package middleware

import (
	"context"

	"github.com/calypr/syfon/internal/access"
)

func MissingGen3AuthHeader(ctx context.Context) bool {
	return access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx)
}

func AuthFailureStatus(ctx context.Context) int {
	return access.AuthStatusCode(ctx)
}
