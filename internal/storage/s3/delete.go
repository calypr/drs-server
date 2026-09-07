package s3

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/calypr/syfon/internal/storage"
)

const maxDeleteObjects = 1000

func (s *backend) Delete(ctx context.Context, targets []storage.PhysicalTarget) error {
	byBucket := make(map[string][]string)
	for _, target := range targets {
		bucket := strings.TrimSpace(target.Bucket)
		key := strings.Trim(strings.TrimSpace(target.Key), "/")
		if bucket == "" || key == "" {
			continue
		}
		byBucket[bucket] = append(byBucket[bucket], key)
	}
	buckets := make([]string, 0, len(byBucket))
	for bucket := range byBucket {
		buckets = append(buckets, bucket)
	}
	sort.Strings(buckets)
	for _, bucket := range buckets {
		keys := dedupeSorted(byBucket[bucket])
		if err := s.deleteBucket(ctx, bucket, keys); err != nil {
			return err
		}
	}
	return nil
}

func (s *backend) deleteBucket(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return err
	}
	if len(keys) == 1 {
		_, err := clients.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(keys[0]),
		})
		return err
	}
	for start := 0; start < len(keys); start += maxDeleteObjects {
		end := start + maxDeleteObjects
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(key)})
		}
		output, err := clients.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return err
		}
		if output != nil && len(output.Errors) > 0 {
			return fmt.Errorf("s3 bulk delete failed for bucket %s: %s", bucket, formatDeleteErrors(output.Errors))
		}
	}
	return nil
}

func dedupeSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func formatDeleteErrors(errors []types.Error) string {
	const maxErrors = 5
	parts := make([]string, 0, len(errors))
	for index, item := range errors {
		if index >= maxErrors {
			parts = append(parts, fmt.Sprintf("and %d more", len(errors)-maxErrors))
			break
		}
		key := strings.TrimSpace(aws.ToString(item.Key))
		code := strings.TrimSpace(aws.ToString(item.Code))
		message := strings.TrimSpace(aws.ToString(item.Message))
		switch {
		case key != "" && code != "" && message != "":
			parts = append(parts, fmt.Sprintf("%s: %s: %s", key, code, message))
		case key != "" && code != "":
			parts = append(parts, fmt.Sprintf("%s: %s", key, code))
		case code != "":
			parts = append(parts, code)
		default:
			parts = append(parts, "unknown delete error")
		}
	}
	return strings.Join(parts, "; ")
}
