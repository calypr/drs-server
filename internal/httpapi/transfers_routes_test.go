package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/services"
	clienttransfer "github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

type multipartTestClient struct{ app *fiber.App }

func (c multipartTestClient) Do(req *http.Request) (*http.Response, error) { return c.app.Test(req) }

type multipartTestScope struct{ prefix string }

func (s *multipartTestScope) LookupBucketScope(_ context.Context, _, project string) (buckets.Scope, bool, error) {
	if project == "" {
		return buckets.Scope{}, false, nil
	}
	return buckets.Scope{Bucket: "physical-bucket", PathPrefix: s.prefix}, true, nil
}

func TestMultipartCompletionReturnsOriginalScopedLocationThroughClient(t *testing.T) {
	provider := &lfsTestStorage{}
	scope := &multipartTestScope{prefix: "original-prefix"}
	service := transfers.NewService(transfers.Dependencies{
		Objects: objects.NewService(newDRSObjectStore(t, nil)), Storage: provider, Scopes: scope,
	})
	app := fiber.New()
	internalapi.RegisterHandlers(app, &internalServer{transfers: service})
	gen, err := internalapi.NewClientWithResponses("http://syfon.test", internalapi.WithHTTPClient(multipartTestClient{app}))
	if err != nil {
		t.Fatal(err)
	}
	client := services.NewDataService(gen, nil, nil, nil)
	uploadID, _, err := client.InitMultipartUploadWithMetadata(context.Background(), "requested-id", "payload.bin", "physical-bucket", common.FileMetadata{Authorizations: map[string][]string{"org": {"project"}}})
	if err != nil {
		t.Fatal(err)
	}
	scope.prefix = "changed-prefix"
	location, err := client.MultipartCompleteWithLocation(context.Background(), "payload.bin", uploadID, []clienttransfer.MultipartPart{{PartNumber: 1, ETag: "etag"}})
	if err != nil {
		t.Fatal(err)
	}
	if location != "s3://physical-bucket/original-prefix/requested-id" {
		t.Fatalf("completed location = %q", location)
	}
	if provider.complete.Target.Key != "original-prefix/requested-id" {
		t.Fatalf("provider completed key = %q", provider.complete.Target.Key)
	}
}

func TestMultipartRoutesRejectInvalidParts(t *testing.T) {
	app := fiber.New()
	internalapi.RegisterHandlers(app, &internalServer{transfers: transfers.NewService(transfers.Dependencies{})})
	tests := []struct {
		name string
		path string
		body any
	}{
		{name: "sign zero", path: "/data/multipart/upload", body: internalapi.InternalMultipartUploadRequest{UploadId: "missing", PartNumber: 0}},
		{name: "complete empty", path: "/data/multipart/complete", body: internalapi.InternalMultipartCompleteRequest{UploadId: "missing", Parts: []internalapi.InternalMultipartPart{}}},
		{name: "complete duplicate", path: "/data/multipart/complete", body: internalapi.InternalMultipartCompleteRequest{UploadId: "missing", Parts: []internalapi.InternalMultipartPart{{PartNumber: 1}, {PartNumber: 1}}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(testCase.body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := app.Test(httptest.NewRequest(http.MethodPost, testCase.path, bytes.NewReader(body)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			payload, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusBadRequest || !bytes.Contains(payload, []byte(errorapi.ErrorCodeInvalidInput)) {
				t.Fatalf("status=%d body=%s", response.StatusCode, payload)
			}
		})
	}
}
