package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/calypr/syfon/internal/storage"
)

func (s *backend) Sign(ctx context.Context, binding storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	if request.Range != nil {
		return s.signDownloadPart(ctx, binding, request)
	}
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	if methodIsPut(request.Method) {
		presigned, err := clients.presigner.PresignPutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(request.Target.PhysicalBucket),
			Key:    aws.String(request.Target.Key),
		}, func(presign *awss3.PresignOptions) {
			presign.Expires = expiry(request.ExpiresIn)
		})
		if err != nil {
			return storage.SignedAccess{}, err
		}
		return storage.SignedAccess{Location: presigned.URL}, nil
	}

	presigned, err := clients.presigner.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket:                     aws.String(request.Target.PhysicalBucket),
		Key:                        aws.String(request.Target.Key),
		ResponseContentDisposition: responseContentDisposition(request.DownloadFilename),
	}, func(presign *awss3.PresignOptions) {
		presign.Expires = expiry(request.ExpiresIn)
	})
	if err != nil {
		return storage.SignedAccess{}, err
	}
	return storage.SignedAccess{Location: presigned.URL}, nil
}

func (s *backend) signDownloadPart(ctx context.Context, binding storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	clients, err := s.getClients(ctx, binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	presigned, err := clients.presigner.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket:                     aws.String(request.Target.PhysicalBucket),
		Key:                        aws.String(request.Target.Key),
		Range:                      aws.String(fmt.Sprintf("bytes=%d-%d", request.Range.Start, request.Range.End)),
		ResponseContentDisposition: responseContentDisposition(request.DownloadFilename),
	}, func(presign *awss3.PresignOptions) {
		presign.Expires = expiry(request.ExpiresIn)
	})
	if err != nil {
		return storage.SignedAccess{}, err
	}
	return storage.SignedAccess{Location: presigned.URL}, nil
}
