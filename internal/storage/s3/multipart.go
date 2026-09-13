package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/calypr/syfon/internal/storage"
)

func (s *backend) BeginMultipart(ctx context.Context, binding storage.ProviderBinding, request storage.BeginMultipartRequest) (storage.UploadID, error) {
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return "", err
	}

	metadata := map[string]string{}
	if strings.TrimSpace(request.CompletionID) != "" {
		metadata[storage.MultipartCompletionMarkerMetadataKey] = request.CompletionID
	}
	output, err := clients.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket:   aws.String(request.Target.PhysicalBucket),
		Key:      aws.String(request.Target.Key),
		Metadata: metadata,
	})
	if err != nil {
		return "", fmt.Errorf("failed to init s3 multipart upload: %w", err)
	}
	if output == nil || strings.TrimSpace(aws.ToString(output.UploadId)) == "" {
		return "", fmt.Errorf("failed to init s3 multipart upload: provider returned an empty upload ID")
	}
	return storage.UploadID(aws.ToString(output.UploadId)), nil
}

func (s *backend) SignMultipartPart(ctx context.Context, binding storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	result, err := clients.presigner.PresignUploadPart(ctx, &awss3.UploadPartInput{
		Bucket:     aws.String(request.Target.PhysicalBucket),
		Key:        aws.String(request.Target.Key),
		UploadId:   aws.String(string(request.UploadID)),
		PartNumber: aws.Int32(request.PartNumber),
	}, func(options *awss3.PresignOptions) {
		options.Expires = expiry(request.ExpiresIn)
	})
	if err != nil {
		return storage.SignedAccess{}, fmt.Errorf("failed to sign s3 multipart part: %w", err)
	}
	return storage.SignedAccess{Location: result.URL}, nil
}

func (s *backend) CompleteMultipart(ctx context.Context, binding storage.ProviderBinding, request storage.CompleteMultipartRequest) error {
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return err
	}
	if matched, err := s.multipartCompletionMatches(ctx, clients.client, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		return nil
	}

	completedParts := make([]types.CompletedPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(part.PartNumber),
		})
	}
	_, err = clients.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:   aws.String(request.Target.PhysicalBucket),
		Key:      aws.String(request.Target.Key),
		UploadId: aws.String(string(request.UploadID)),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		completionErr := fmt.Errorf("failed to complete s3 multipart upload: %w", err)
		matched, reconcileErr := s.multipartCompletionMatches(ctx, clients.client, request.Target, request.CompletionID)
		if reconcileErr != nil {
			return errors.Join(completionErr, reconcileErr)
		}
		if matched {
			return nil
		}
		return completionErr
	}
	return nil
}
