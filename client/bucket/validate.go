package bucket

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	bucketapi "github.com/calypr/syfon/apigen/bucketapi"
)

// ValidateBucket verifies that the bucket exists and is accessible using native provider SDKs.
func ValidateBucket(ctx context.Context, req bucketapi.PutBucketRequest) error {
	provider := "s3"
	if req.Provider != nil {
		provider = strings.ToLower(*req.Provider)
	}

	switch provider {
	case "s3":
		return validateS3(ctx, req)
	default:
		return nil
	}
}

func validateS3(ctx context.Context, req bucketapi.PutBucketRequest) error {
	var opts []func(*config.LoadOptions) error
	if req.Region != nil {
		opts = append(opts, config.WithRegion(*req.Region))
	}
	if req.AccessKey != nil && req.SecretKey != nil {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(*req.AccessKey, *req.SecretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to load s3 config: %w", err)
	}

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if req.Endpoint != nil {
			o.BaseEndpoint = aws.String(*req.Endpoint)
		}
	})

	if _, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(req.Bucket)}); err != nil {
		return fmt.Errorf("s3 bucket validation failed for %s: %w", req.Bucket, err)
	}
	return nil
}
