package s3

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/storage"
)

type fakeListPage struct {
	output *awss3.ListObjectsV2Output
	err    error
}

type fakeListClient struct {
	pages       map[string][]fakeListPage
	calls       []string
	startAfters []string
}

func (client *fakeListClient) ListObjectsV2(_ context.Context, input *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	token := strings.TrimSpace(aws.ToString(input.ContinuationToken))
	startAfter := strings.TrimSpace(aws.ToString(input.StartAfter))
	client.calls = append(client.calls, token)
	client.startAfters = append(client.startAfters, startAfter)
	queue := client.pages[token]
	if len(queue) == 0 {
		return nil, errors.New("unexpected token " + token)
	}
	next := queue[0]
	client.pages[token] = queue[1:]
	return next.output, next.err
}

func noRetrySleep(t *testing.T) func() {
	t.Helper()
	previous := sleepListPageRetry
	sleepListPageRetry = func(context.Context, time.Duration) error { return nil }
	return func() { sleepListPageRetry = previous }
}

func testListPages(t *testing.T, provider *backend, client *fakeListClient, request storage.InventoryRequest) ([]storage.ObjectMetadata, listStats, error) {
	t.Helper()
	input := &awss3.ListObjectsV2Input{Bucket: aws.String(request.Target.Bucket), Prefix: aws.String(request.Target.Prefix)}
	items, stats, _, err := provider.listPages(context.Background(), client, input, request.Target.Bucket, request.Target.Prefix, request.Target.Prefix, request, false)
	return items, stats, err
}

func TestListPagesRetriesTransientPageFailure(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "4")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{output: testListPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{output: testFinalListPage("prefix/two.txt")},
			{output: testFinalListPage("prefix/two.txt")},
			{output: testFinalListPage("prefix/two.txt")},
		},
	}}
	provider := newBackend(nil)
	items, stats, err := testListPages(t, provider, client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if got := metadataKeys(items); strings.Join(got, ",") != "prefix/one.txt,prefix/two.txt" {
		t.Fatalf("unexpected keys: %v", got)
	}
	if got := countToken(client.calls, "token-2"); got != 5 {
		t.Fatalf("expected page retry and terminal replays to reuse continuation token five times, got calls %q", strings.Join(client.calls, ","))
	}
	if stats.Pages != 2 {
		t.Fatalf("expected 2 successful pages, got %+v", stats)
	}
}

func TestListPagesRecoversObservedPageSeventeenInternalError(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "4")
	client := &fakeListClient{pages: map[string][]fakeListPage{}}
	for page := 1; page < 17; page++ {
		token := ""
		if page > 1 {
			token = testTokenForPage(page)
		}
		client.pages[token] = []fakeListPage{{output: testListPage(testTokenForPage(page+1), "prefix/page-"+strconv.Itoa(page)+".txt")}}
	}
	client.pages[testTokenForPage(17)] = []fakeListPage{
		{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		{output: testFinalListPage("prefix/page-17.txt")},
		{output: testFinalListPage("prefix/page-17.txt")},
		{output: testFinalListPage("prefix/page-17.txt")},
	}
	provider := newBackend(nil)
	items, stats, err := testListPages(t, provider, client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(items) != 17 {
		t.Fatalf("expected 17 inventory objects after recovered page 17, got %d", len(items))
	}
	if stats.Pages != 17 || stats.Retries != 2 {
		t.Fatalf("expected recovered page 17 with 2 retries, got %+v", stats)
	}
	if got := countToken(client.calls, testTokenForPage(17)); got != 5 {
		t.Fatalf("expected page 17 token to be retried and replayed five times, got calls %v", client.calls)
	}
}

func TestListPagesExhaustsTransientRetriesWithProgress(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "3")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{output: testListPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		},
	}}
	items, stats, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err == nil {
		t.Fatal("expected retry exhaustion error")
	}
	if len(items) != 1 || items[0].Key != "prefix/one.txt" {
		t.Fatalf("expected collected page to be preserved for error reporting, got %+v", items)
	}
	if stats.FailedPage != 2 {
		t.Fatalf("expected failed page 2, got %+v", stats)
	}
	if !strings.Contains(err.Error(), "page 2") || !strings.Contains(err.Error(), "after 1 objects and 3 attempts") {
		t.Fatalf("expected progress in error, got %q", err.Error())
	}
}

func TestListPagesDoesNotRetryAccessDenied(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "4")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}}},
	}}
	_, _, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err == nil {
		t.Fatal("expected access denied error")
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected no retries for access denied, got calls %v", client.calls)
	}
}

func TestListPagesErrorsOnMissingContinuationToken(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "1")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{output: &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(true), Contents: []types.Object{{Key: aws.String("prefix/one.txt")}}}}},
	}}
	_, _, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err == nil || !strings.Contains(err.Error(), "without next continuation token") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestListPagesRetriesMalformedTruncatedPage(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envListPageMaxAttempts, "2")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {
			{output: &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(true)}},
			{output: testFinalListPage("prefix/one.txt")},
			{output: testFinalListPage("prefix/one.txt")},
			{output: testFinalListPage("prefix/one.txt")},
		},
	}}
	items, stats, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(items) != 1 || stats.Retries != 1 {
		t.Fatalf("expected malformed page retry, items=%v stats=%+v", items, stats)
	}
}

func TestListPagesRetriesExactProbeMiss(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envExactProbeMaxAttempts, "2")
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {
			{output: testFinalListPage("prefix/other.txt")},
			{output: testFinalListPage("prefix/target.txt")},
		},
	}}
	request := storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix/target.txt"}, ExactPrefix: true, MaxKeys: 1}
	items, _, _, err := newBackend(nil).listPagesWithExactProbeRetry(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/target.txt")}, "bucket", "prefix/target.txt", "prefix/target.txt", request, false)
	if err != nil {
		t.Fatalf("exact probe: %v", err)
	}
	if len(items) != 1 || items[0].Key != "prefix/target.txt" {
		t.Fatalf("expected exact probe retry to find target, got %v", items)
	}
}

func TestListPagesTerminalReplayUsesContinuationTokenOnly(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{output: testListPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{output: testFinalListPage("prefix/two.txt")},
			{output: testFinalListPage("prefix/two.txt")},
			{output: testFinalListPage("prefix/two.txt")},
		},
	}}
	items, stats, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if got := strings.Join(metadataKeys(items), ","); got != "prefix/one.txt,prefix/two.txt" {
		t.Fatalf("unexpected keys: got %q", got)
	}
	if stats.TerminalReplayAttempts != 2 {
		t.Fatalf("expected two terminal replays, got %+v", stats)
	}
	if len(client.startAfters) != 4 || containsNonEmptyToken(client.startAfters) {
		t.Fatalf("expected no StartAfter requests, got %q", client.startAfters)
	}
}

func TestListPagesRejectsContradictoryTerminalReplay(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	client := &fakeListClient{pages: map[string][]fakeListPage{
		"": {{output: testListPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{output: testFinalListPage("prefix/two.txt")},
			{output: testListPage("token-3", "prefix/two.txt")},
		},
	}}
	_, _, err := testListPages(t, newBackend(nil), client, storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err == nil || !strings.Contains(err.Error(), "terminal replay") {
		t.Fatalf("expected contradictory terminal replay error, got %v", err)
	}
}

func TestInventoryIncludeHeadFailureDropsListedItems(t *testing.T) {
	client := &fakeClient{
		listOutputs: []*awss3.ListObjectsV2Output{
			testFinalListPage("prefix/one.txt"),
			testFinalListPage("prefix/one.txt"),
			testFinalListPage("prefix/one.txt"),
		},
		headErrs: []error{&smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}},
	}
	provider := cachedBackend(client, &fakePresigner{})
	result, err := provider.Inventory(context.Background(), storage.InventoryRequest{
		Target:      storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"},
		IncludeHead: true,
	})
	if err == nil {
		t.Fatal("expected IncludeHead error")
	}
	if len(result.Items) != 0 || result.Complete {
		t.Fatalf("IncludeHead preserved items unexpectedly: %#v", result)
	}
	var operation *storage.OperationError
	if !errors.As(err, &operation) || operation.Kind != storage.ErrorUnavailable {
		t.Fatalf("IncludeHead error = %v, want typed provider error", err)
	}
}

func testListPage(nextToken string, keys ...string) *awss3.ListObjectsV2Output {
	return &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(true), NextContinuationToken: aws.String(nextToken), Contents: testInventoryObjects(keys...)}
}

func testFinalListPage(keys ...string) *awss3.ListObjectsV2Output {
	return &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(false), Contents: testInventoryObjects(keys...)}
}

func testInventoryObjects(keys ...string) []types.Object {
	objects := make([]types.Object, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, types.Object{Key: aws.String(key), Size: aws.Int64(12)})
	}
	return objects
}

func testTokenForPage(page int) string { return "token-" + strconv.Itoa(page) }

func metadataKeys(items []storage.ObjectMetadata) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func countToken(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func containsNonEmptyToken(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
