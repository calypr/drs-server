package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/client/transfer"
)

func TestMultipartCompletionLocationAndLegacyResponses(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body, want string
		wantError                     bool
	}{
		{name: "resolved target", contentType: "application/json", body: `{"object_url":"s3://bucket/project/key"}`, want: "s3://bucket/project/key"},
		{name: "legacy server", contentType: "text/plain", body: "OK"},
		{name: "invalid location", contentType: "application/json", body: `{"object_url":42}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/data/multipart/complete" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", test.contentType)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client := NewDataService(mustInternalClient(t, server.URL), nil, nil, nil)
			got, err := client.MultipartCompleteWithLocation(context.Background(), "key", "upload-id", []transfer.MultipartPart{{PartNumber: 1, ETag: "etag"}})
			if (err != nil) != test.wantError {
				t.Fatalf("completion error = %v, wantError=%v", err, test.wantError)
			}
			if got != test.want {
				t.Fatalf("completion location = %q, want %q", got, test.want)
			}
		})
	}
}
