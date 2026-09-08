package bucketapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type scopeBindingServer struct {
	ServerInterface
	bucket string
	params DeleteBucketScopeParams
	called bool
}

func (s *scopeBindingServer) DeleteBucketScope(c fiber.Ctx, bucket string, params DeleteBucketScopeParams) error {
	s.bucket, s.params, s.called = bucket, params, true
	return c.SendStatus(http.StatusNoContent)
}

func TestGeneratedScopeParametersPreserveFiberValues(t *testing.T) {
	for _, tc := range []struct {
		target       string
		status       int
		bucket       string
		organization string
		path         string
	}{
		{"/data/buckets/bucket/scopes?organization=org&path=", 204, "bucket", "org", ""},
		{"/data/buckets/bucket/scopes?organization=org", 400, "", "", ""},
		{"/data/buckets/bucket/scopes?path=", 400, "", "", ""},
		{"/data/buckets/foo+bar/scopes?organization=org%2Bname&path=100%25", 204, "foo+bar", "org+name", "100%"},
		{"/data/buckets/foo%2Fbar/scopes?organization=org&path=%252F", 204, "foo%2Fbar", "org", "%2F"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			server := &scopeBindingServer{}
			app := fiber.New()
			RegisterHandlers(app, server)
			resp, err := app.Test(httptest.NewRequest(http.MethodDelete, tc.target, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d, want %d", resp.StatusCode, tc.status)
			}
			if tc.status == 204 {
				if !server.called || server.bucket != tc.bucket || server.params.Organization != tc.organization || server.params.Path != tc.path {
					t.Fatalf("binding changed: called=%v bucket=%q params=%+v", server.called, server.bucket, server.params)
				}
			} else if server.called {
				t.Fatal("invalid request reached the operation")
			}
		})
	}
}
