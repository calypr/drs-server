package listcmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/internalapi"
	syfonclient "github.com/calypr/syfon/client/services"
)

type fakeIndexLister struct {
	responses map[string]internalapi.ListRecordsResponse
	calls     []syfonclient.ListRecordsOptions
}

func (f *fakeIndexLister) List(_ context.Context, opts syfonclient.ListRecordsOptions) (internalapi.ListRecordsResponse, error) {
	f.calls = append(f.calls, opts)
	key := fmt.Sprintf("%s|%s|%d|%s", opts.Organization, opts.ProjectID, opts.Limit, opts.Start)
	if resp, ok := f.responses[key]; ok {
		return resp, nil
	}
	return internalapi.ListRecordsResponse{}, fmt.Errorf("unexpected list call: %s", key)
}

func TestListRecordsNonRecursive(t *testing.T) {
	rootRecords := []internalapi.InternalRecord{
		{Did: "did-root", Name: ptr("README.md")},
		{Did: "did-data", Name: ptr("file1.txt")},
	}
	lister := &fakeIndexLister{
		responses: map[string]internalapi.ListRecordsResponse{
			"Ellrott_Lab|hla2vec|10|": {Records: &rootRecords},
		},
	}

	records, err := listRecords(context.Background(), lister, syfonclient.ListRecordsOptions{
		Organization: "Ellrott_Lab",
		ProjectID:    "hla2vec",
		Limit:        10,
	}, false, "")
	if err != nil {
		t.Fatalf("listRecords returned error: %v", err)
	}

	got := make([]string, 0, len(records))
	for _, rec := range records {
		got = append(got, strings.TrimSpace(rec.Did))
	}
	want := []string{"did-root", "did-data"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected record set: got %v want %v", got, want)
	}
}

func TestListRecordsRejectsRecursive(t *testing.T) {
	_, err := listRecords(context.Background(), &fakeIndexLister{}, syfonclient.ListRecordsOptions{
		Organization: "Ellrott_Lab",
		ProjectID:    "hla2vec",
		Limit:        10,
	}, true, "")
	if err == nil || !strings.Contains(err.Error(), "path-based recursive listing is no longer supported") {
		t.Fatalf("expected recursive unsupported error, got %v", err)
	}
}

func TestListRecordsRejectsPathFilter(t *testing.T) {
	_, err := listRecords(context.Background(), &fakeIndexLister{}, syfonclient.ListRecordsOptions{Limit: 10}, false, "nested")
	if err == nil || !strings.Contains(err.Error(), "path-based listing is no longer supported") {
		t.Fatalf("expected path unsupported error, got %v", err)
	}
}

func ptr(v string) *string { return &v }
