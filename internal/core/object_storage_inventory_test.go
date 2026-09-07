package core

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
)

type fakeS3ListPage struct {
	output *awss3.ListObjectsV2Output
	err    error
}

type fakeS3ListClient struct {
	pages           map[string][]fakeS3ListPage
	startAfterPages map[string][]fakeS3ListPage
	calls           []string
	startAfters     []string
}

func (client *fakeS3ListClient) ListObjectsV2(_ context.Context, input *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	token := strings.TrimSpace(aws.ToString(input.ContinuationToken))
	startAfter := strings.TrimSpace(aws.ToString(input.StartAfter))
	client.calls = append(client.calls, token)
	client.startAfters = append(client.startAfters, startAfter)
	if startAfter != "" {
		queue := client.startAfterPages[startAfter]
		if len(queue) == 0 {
			return finalListPage(), nil
		}
		next := queue[0]
		client.startAfterPages[startAfter] = queue[1:]
		return next.output, next.err
	}
	queue := client.pages[token]
	if len(queue) == 0 {
		return nil, errors.New("unexpected token " + token)
	}
	next := queue[0]
	client.pages[token] = queue[1:]
	return next.output, next.err
}

func TestListS3PrefixPagesRetriesTransientPageFailure(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "4")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{output: listPage("token-2", "prefix/one.txt")},
		},
		"token-2": {
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{output: finalListPage("prefix/two.txt")},
			{output: finalListPage("prefix/two.txt")},
			{output: finalListPage("prefix/two.txt")},
		},
	}}

	items, stats, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if got, want := keys(items), []string{"prefix/one.txt", "prefix/two.txt"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected keys: got %v want %v", got, want)
	}
	if got := countString(client.calls, "token-2"); got != 5 {
		t.Fatalf("expected page retry and terminal replays to reuse continuation token five times, got calls %q", strings.Join(client.calls, ","))
	}
	if stats.Pages != 2 {
		t.Fatalf("expected 2 successful pages, got %+v", stats)
	}
}

func TestListS3PrefixPagesRecoversObservedPageSeventeenInternalError(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "4")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{}}
	for page := 1; page < 17; page++ {
		token := ""
		if page > 1 {
			token = tokenForPage(page)
		}
		client.pages[token] = []fakeS3ListPage{
			{output: listPage(tokenForPage(page+1), "prefix/page-"+strconv.Itoa(page)+".txt")},
		}
	}
	client.pages[tokenForPage(17)] = []fakeS3ListPage{
		{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		{output: finalListPage("prefix/page-17.txt")},
		{output: finalListPage("prefix/page-17.txt")},
		{output: finalListPage("prefix/page-17.txt")},
	}

	items, stats, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(items) != 17 {
		t.Fatalf("expected 17 inventory objects after recovered page 17, got %d", len(items))
	}
	if stats.Pages != 17 || stats.Retries != 2 {
		t.Fatalf("expected recovered page 17 with 2 retries, got %+v", stats)
	}
	if got := countString(client.calls, tokenForPage(17)); got != 5 {
		t.Fatalf("expected page 17 token to be retried and replayed five times, got calls %v", client.calls)
	}
}

func TestListS3PrefixPagesExhaustsTransientRetriesWithProgress(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "3")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{output: listPage("token-2", "prefix/one.txt")},
		},
		"token-2": {
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
			{err: &smithy.GenericAPIError{Code: "InternalError", Message: "try again"}},
		},
	}}

	items, stats, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
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

func TestListS3PrefixPagesDoesNotRetryAccessDenied(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "4")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}},
		},
	}}

	_, _, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err == nil {
		t.Fatal("expected access denied error")
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected no retries for access denied, got calls %v", client.calls)
	}
}

func TestListS3PrefixPagesErrorsOnMissingContinuationToken(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "1")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{output: &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(true), Contents: []types.Object{{Key: aws.String("prefix/one.txt")}}}},
		},
	}}

	_, _, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err == nil || !strings.Contains(err.Error(), "without next continuation token") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestListS3PrefixPagesRetriesMalformedTruncatedPage(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ListPageMaxAttempts, "2")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{output: &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(true)}},
			{output: finalListPage("prefix/one.txt")},
			{output: finalListPage("prefix/one.txt")},
			{output: finalListPage("prefix/one.txt")},
		},
	}}

	items, stats, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(items) != 1 || stats.Retries != 1 {
		t.Fatalf("expected malformed page retry, items=%v stats=%+v", items, stats)
	}
}

func TestListS3PrefixPagesRetriesExactProbeMiss(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3ExactProbeMaxAttempts, "2")
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {
			{output: finalListPage("prefix/other.txt")},
			{output: finalListPage("prefix/target.txt")},
		},
	}}

	items, _, _, err := listS3PrefixPagesWithExactProbeRetry(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/target.txt")}, "bucket", "prefix/target.txt", "prefix/target.txt", StoragePrefixListOptions{ExactPrefix: true, MaxKeys: 1}, false)
	if err != nil {
		t.Fatalf("exact probe: %v", err)
	}
	if len(items) != 1 || items[0].Key != "prefix/target.txt" {
		t.Fatalf("expected exact probe retry to find target, got %v", items)
	}
}

func TestListS3PrefixPagesTerminalReplayUsesContinuationTokenOnly(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {{output: listPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{output: finalListPage("prefix/two.txt")},
			{output: finalListPage("prefix/two.txt")},
			{output: finalListPage("prefix/two.txt")},
		},
	}}

	items, stats, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if got, want := strings.Join(keys(items), ","), "prefix/one.txt,prefix/two.txt"; got != want {
		t.Fatalf("unexpected keys: got %q want %q", got, want)
	}
	if stats.TerminalReplayAttempts != 2 {
		t.Fatalf("expected two terminal replays, got %+v", stats)
	}
	if len(client.startAfters) != 4 || containsNonEmpty(client.startAfters) {
		t.Fatalf("expected no StartAfter requests, got %q", client.startAfters)
	}
}

func TestListS3PrefixPagesRejectsContradictoryTerminalReplay(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	client := &fakeS3ListClient{pages: map[string][]fakeS3ListPage{
		"": {{output: listPage("token-2", "prefix/one.txt")}},
		"token-2": {
			{output: finalListPage("prefix/two.txt")},
			{output: listPage("token-3", "prefix/two.txt")},
		},
	}}

	_, _, _, err := listS3PrefixPages(context.Background(), client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("prefix/")}, "bucket", "prefix", "prefix/", StoragePrefixListOptions{}, false)
	if err == nil || !strings.Contains(err.Error(), "terminal replay") {
		t.Fatalf("expected contradictory terminal replay error, got %v", err)
	}
}

func listPage(nextToken string, keys ...string) *awss3.ListObjectsV2Output {
	return &awss3.ListObjectsV2Output{
		IsTruncated:           aws.Bool(true),
		NextContinuationToken: aws.String(nextToken),
		Contents:              objects(keys...),
	}
}

func finalListPage(keys ...string) *awss3.ListObjectsV2Output {
	return &awss3.ListObjectsV2Output{
		IsTruncated: aws.Bool(false),
		Contents:    objects(keys...),
	}
}

func objects(keys ...string) []types.Object {
	out := make([]types.Object, 0, len(keys))
	for _, key := range keys {
		out = append(out, types.Object{Key: aws.String(key), Size: aws.Int64(12)})
	}
	return out
}

func tokenForPage(page int) string {
	return "token-" + strconv.Itoa(page)
}

func keys(items []StorageBucketObject) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Key)
	}
	return out
}

func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func containsString(values []string, target string) bool {
	return countString(values, target) > 0
}

func containsNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func disableS3ListRetrySleep(t *testing.T) func() {
	t.Helper()
	previous := sleepS3ListPageRetry
	sleepS3ListPageRetry = func(context.Context, time.Duration) error {
		return nil
	}
	return func() {
		sleepS3ListPageRetry = previous
	}
}
