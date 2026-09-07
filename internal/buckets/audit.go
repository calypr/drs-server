package buckets

import (
	"context"
	"log/slog"

	"github.com/calypr/syfon/internal/access"
)

// AuditCredentialAccess logs credential access events with request and auth
// mode context. The fields and result policy intentionally match the legacy
// S3 credential audit contract.
func AuditCredentialAccess(ctx context.Context, requestID string, action string, bucket string, err error) {
	mode := "local"
	if access.FromContext(ctx).Mode == "gen3" {
		mode = "gen3"
	}
	if err != nil {
		slog.Warn("s3 credential audit", "action", action, "bucket", bucket, "request_id", requestID, "mode", mode, "result", "error", "err", err)
		return
	}
	slog.Info("s3 credential audit", "action", action, "bucket", bucket, "request_id", requestID, "mode", mode, "result", "success")
}
