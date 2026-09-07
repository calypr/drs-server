package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/calypr/syfon/internal/storage"
)

func (s *backend) InitMultipartUpload(ctx context.Context, target storage.ObjectTarget) (storage.UploadID, error) {
	clients, err := s.getClients(ctx, target.Bucket)
	if err != nil {
		return "", err
	}

	output, err := clients.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(target.Bucket),
		Key:    aws.String(target.Key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to init s3 multipart upload: %w", err)
	}
	// aws.ToString intentionally converts a nil UploadId to an empty success,
	// preserving the old signer contract.
	return storage.UploadID(aws.ToString(output.UploadId)), nil
}

func (s *backend) SignMultipartPart(ctx context.Context, request storage.MultipartPartRequest) (storage.Access, error) {
	clients, err := s.getClients(ctx, request.Target.Bucket)
	if err != nil {
		return storage.Access{}, err
	}

	result, err := clients.presigner.PresignUploadPart(ctx, &awss3.UploadPartInput{
		Bucket:     aws.String(request.Target.Bucket),
		Key:        aws.String(request.Target.Key),
		UploadId:   aws.String(string(request.UploadID)),
		PartNumber: aws.Int32(request.PartNumber),
	}, func(options *awss3.PresignOptions) {
		options.Expires = defaultExpiry
	})
	if err != nil {
		return storage.Access{}, fmt.Errorf("failed to sign s3 multipart part: %w", err)
	}
	return storage.Access{Location: result.URL}, nil
}

func (s *backend) CompleteMultipartUpload(ctx context.Context, request storage.CompleteMultipartRequest) error {
	clients, err := s.getClients(ctx, request.Target.Bucket)
	if err != nil {
		return err
	}

	completedParts := make([]types.CompletedPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(part.PartNumber),
		})
	}
	_, err = clients.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:   aws.String(request.Target.Bucket),
		Key:      aws.String(request.Target.Key),
		UploadId: aws.String(string(request.UploadID)),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete s3 multipart upload: %w", err)
	}
	return nil
}
