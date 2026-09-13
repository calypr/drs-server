package s3

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/storage"
)

type multipartAbortClient interface {
	AbortMultipartUpload(context.Context, *awss3.AbortMultipartUploadInput, ...func(*awss3.Options)) (*awss3.AbortMultipartUploadOutput, error)
}

func (s *backend) AbortMultipart(ctx context.Context, binding storage.ProviderBinding, request storage.AbortMultipartRequest) error {
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return err
	}
	aborter, ok := clients.client.(multipartAbortClient)
	if !ok {
		return errors.New("s3 client does not support multipart abort")
	}
	_, err = aborter.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket:   aws.String(request.Target.PhysicalBucket),
		Key:      aws.String(request.Target.Key),
		UploadId: aws.String(string(request.UploadID)),
	})
	if err != nil {
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
			return nil
		}
		var genericErr *smithy.GenericAPIError
		if errors.As(err, &genericErr) && genericErr.Code == "NoSuchUpload" {
			return nil
		}
		return fmt.Errorf("failed to abort s3 multipart upload: %w", err)
	}
	return nil
}
