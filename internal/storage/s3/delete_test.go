package s3

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/calypr/syfon/internal/storage"
)

func TestDeleteSingleUsesDeleteObject(t *testing.T) {
	client := &fakeClient{}
	provider := cachedBackend(client, &fakePresigner{})
	if err := provider.Delete(context.Background(), []storage.PhysicalTarget{{Bucket: "bucket", Key: "object"}}); err != nil {
		t.Fatal(err)
	}
	if len(client.deleteInputs) != 1 || len(client.deleteObjects) != 0 {
		t.Fatalf("single delete calls = object=%#v bulk=%#v", client.deleteInputs, client.deleteObjects)
	}
	if got := aws.ToString(client.deleteInputs[0].Bucket); got != "bucket" {
		t.Fatalf("bucket = %q", got)
	}
	if got := aws.ToString(client.deleteInputs[0].Key); got != "object" {
		t.Fatalf("key = %q", got)
	}
}

func TestDeleteSortsDeduplicatesAndChunksAt1000(t *testing.T) {
	client := &fakeClient{}
	provider := cachedBackend(client, &fakePresigner{})
	targets := make([]storage.PhysicalTarget, 0, 1003)
	for index := 1002; index >= 0; index-- {
		targets = append(targets, storage.PhysicalTarget{Bucket: "bucket", Key: fmt.Sprintf("key-%04d", index)})
	}
	targets = append(targets,
		storage.PhysicalTarget{Bucket: "bucket", Key: "key-0001"},
		storage.PhysicalTarget{Bucket: "bucket", Key: "key-1000"},
	)
	if err := provider.Delete(context.Background(), targets); err != nil {
		t.Fatal(err)
	}
	if len(client.deleteObjects) != 2 {
		t.Fatalf("bulk call count = %d, want 2", len(client.deleteObjects))
	}
	first := client.deleteObjects[0].Delete.Objects
	second := client.deleteObjects[1].Delete.Objects
	if len(first) != 1000 || len(second) != 3 {
		t.Fatalf("chunk sizes = %d and %d, want 1000 and 3", len(first), len(second))
	}
	if !aws.ToBool(client.deleteObjects[0].Delete.Quiet) || !aws.ToBool(client.deleteObjects[1].Delete.Quiet) {
		t.Fatal("bulk delete was not quiet")
	}
	for index, object := range first {
		want := fmt.Sprintf("key-%04d", index)
		if got := aws.ToString(object.Key); got != want {
			t.Fatalf("first chunk key[%d] = %q, want %q", index, got, want)
		}
	}
	for index, object := range second {
		want := fmt.Sprintf("key-%04d", index+1000)
		if got := aws.ToString(object.Key); got != want {
			t.Fatalf("second chunk key[%d] = %q, want %q", index, got, want)
		}
	}
}

func TestDeleteFormatsAtMostFivePartialErrors(t *testing.T) {
	client := &fakeClient{deleteOutput: &awss3.DeleteObjectsOutput{Errors: []types.Error{
		{Key: aws.String("one"), Code: aws.String("AccessDenied"), Message: aws.String("first")},
		{Key: aws.String("two"), Code: aws.String("NoSuchKey")},
		{Code: aws.String("InternalError")},
		{Key: aws.String("four")},
		{},
		{Key: aws.String("six"), Code: aws.String("Other"), Message: aws.String("ignored after five")},
	}}}
	provider := cachedBackend(client, &fakePresigner{})
	err := provider.Delete(context.Background(), []storage.PhysicalTarget{{Bucket: "bucket", Key: "a"}, {Bucket: "bucket", Key: "b"}})
	if err == nil {
		t.Fatal("expected partial delete error")
	}
	want := "s3 bulk delete failed for bucket bucket: one: AccessDenied: first; two: NoSuchKey; InternalError; unknown delete error; unknown delete error; and 1 more"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if !strings.Contains(err.Error(), "and 1 more") {
		t.Fatalf("error omitted truncation marker: %v", err)
	}
}
