package buckets

import (
	"context"
	"errors"
	"testing"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
)

func TestBucketPolicyHelpers(t *testing.T) {
	scope := buckets.Scope{
		Organization: "org",
		ProjectID:    "proj",
		Bucket:       "bucket-a",
	}
	resource, _ := clientaccess.ResourcePath("org", "proj")

	t.Run("global bucket control access", func(t *testing.T) {
		if !access.HasAnyMethodAccess(context.Background(), []string{resource}, "read") {
			t.Fatal("expected open access outside enforced authz")
		}
	})

	t.Run("scoped bucket access", func(t *testing.T) {
		ctx := policyTestContext("gen3", true, map[string]map[string]bool{
			resource: {"delete": true, "update": true},
		})

		if !buckets.ScopeAllowed(ctx, scope, "delete") {
			t.Fatal("expected scoped bucket access")
		}
		if !access.HasAnyMethodAccess(ctx, []string{resource}, "delete") {
			t.Fatal("expected resource access")
		}
	})

	t.Run("allowed bucket filtering by scope name", func(t *testing.T) {
		ctx := policyTestContext("gen3", true, map[string]map[string]bool{
			resource: {"read": true},
		})

		if !buckets.BucketsAllowedByNames(ctx, []buckets.Scope{scope}, "bucket-a", "read") {
			t.Fatal("expected bucket name match to be allowed")
		}
		if buckets.BucketsAllowedByNames(ctx, []buckets.Scope{scope}, "bucket-b", "read") {
			t.Fatal("expected non-matching bucket to be denied")
		}
	})

	t.Run("bucket scope write allows org descendant creators", func(t *testing.T) {
		orgResource, _ := clientaccess.ResourcePath("org", "")
		ctx := policyTestContext("gen3", true, map[string]map[string]bool{
			orgResource: {"arborist:create-descendant": true},
		})

		if err := buckets.AuthorizeScopeWrite(ctx, "org", "new-project", "create", "update"); err != nil {
			t.Fatalf("expected org descendant creator to be allowed, got %v", err)
		}
	})

	t.Run("bucket scope write does not allow top-level program creator without org or project ownership", func(t *testing.T) {
		ctx := policyTestContext("gen3", true, map[string]map[string]bool{
			"/programs": {"arborist:create-descendant": true},
		})

		if err := buckets.AuthorizeScopeWrite(ctx, "brand_new_org", "new-project", "create", "update"); err == nil {
			t.Fatal("expected top-level program creator alone to be denied")
		}
	})

	t.Run("bucket scope write does not treat requestor create as arborist create-descendant", func(t *testing.T) {
		orgResource, _ := clientaccess.ResourcePath("org", "")
		ctx := policyTestContext("gen3", true, map[string]map[string]bool{
			orgResource: {"requestor:create": true},
		})

		err := buckets.AuthorizeScopeWrite(ctx, "org", "new-project", "create", "update")
		if err == nil {
			t.Fatal("expected requestor create alone to be denied")
		}
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})
}
