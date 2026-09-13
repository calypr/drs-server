package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/storage"
)

func (s *backend) multipartCompletionMatches(ctx context.Context, client s3Client, target storage.Target, completionID string) (bool, error) {
	if strings.TrimSpace(completionID) == "" {
		return false, nil
	}
	output, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(target.PhysicalBucket),
		Key:    aws.String(target.Key),
	})
	if err != nil {
		if s3ObjectNotFound(err) {
			return false, nil
		}
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect s3 multipart completion marker for %s/%s: %w", target.PhysicalBucket, target.Key, err))
	}
	if output == nil {
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect s3 multipart completion marker for %s/%s: provider returned an empty response", target.PhysicalBucket, target.Key))
	}
	for key, value := range output.Metadata {
		if strings.EqualFold(key, storage.MultipartCompletionMarkerMetadataKey) {
			return value == completionID, nil
		}
	}
	return false, nil
}

func s3ObjectNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
	case "notfound", "nosuchkey":
		return true
	default:
		return false
	}
}
