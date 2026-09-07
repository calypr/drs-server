package services

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAPIResponseErrorPreservesMessages(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"", "unexpected response: 500"},
		{`{"error":{"message":" detail ","type":"failure"},"message":"fallback"}`, "unexpected response: 500: detail"},
		{`{"message":" fallback "}`, "unexpected response: 500: fallback"},
		{" not json ", "unexpected response: 500: not json"},
		{`{"error":{"message":"detail","type":123}}`, `unexpected response: 500: {"error":{"message":"detail","type":123}}`},
	} {
		if got := apiResponseError("unexpected response", 500, []byte(tc.body)).Error(); got != tc.want {
			t.Errorf("body %q: got %q want %q", tc.body, got, tc.want)
		}
	}
}

func TestDRSListVariantsPreserveEmptyArray(t *testing.T) {
	service := NewDRSService(nil, NewIndexService(nil, &fakeRequester{}))
	ctx := context.Background()
	for _, list := range []func() (DRSPage, error){
		func() (DRSPage, error) { return service.ListObjects(ctx, 10, 1) },
		func() (DRSPage, error) { return service.ListObjectsAfter(ctx, 10, "after") },
		func() (DRSPage, error) { return service.ListObjectsByProject(ctx, "project", 10, 1) },
		func() (DRSPage, error) { return service.ListObjectsByProjectAfter(ctx, "project", 10, "after") },
	} {
		page, err := list()
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"drs_objects":[]}` {
			t.Fatalf("empty list JSON changed: %s", data)
		}
	}
}
