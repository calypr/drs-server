package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/calypr/syfon/internal/storage"
)

func (s *backend) SignURL(ctx context.Context, target storage.ObjectTarget, options storage.AccessOptions) (storage.Access, error) {
	clients, err := s.getClients(ctx, target.Bucket)
	if err != nil {
		return storage.Access{}, err
	}

	if methodIsPut(options) {
		request, err := clients.presigner.PresignPutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(target.Bucket),
			Key:    aws.String(target.Key),
		}, func(presign *awss3.PresignOptions) {
			presign.Expires = expiry(options)
		})
		if err != nil {
			return storage.Access{}, err
		}
		return storage.Access{Location: request.URL}, nil
	}

	request, err := clients.presigner.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket:                     aws.String(target.Bucket),
		Key:                        aws.String(target.Key),
		ResponseContentDisposition: responseContentDisposition(options.DownloadFilename),
	}, func(presign *awss3.PresignOptions) {
		presign.Expires = expiry(options)
	})
	if err != nil {
		return storage.Access{}, err
	}
	return storage.Access{Location: request.URL}, nil
}

func (s *backend) SignDownloadPart(ctx context.Context, target storage.ObjectTarget, byteRange storage.ByteRange, options storage.AccessOptions) (storage.Access, error) {
	clients, err := s.getClients(ctx, target.Bucket)
	if err != nil {
		return storage.Access{}, err
	}

	request, err := clients.presigner.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket:                     aws.String(target.Bucket),
		Key:                        aws.String(target.Key),
		Range:                      aws.String(fmt.Sprintf("bytes=%d-%d", byteRange.Start, byteRange.End)),
		ResponseContentDisposition: responseContentDisposition(options.DownloadFilename),
	}, func(presign *awss3.PresignOptions) {
		presign.Expires = expiry(options)
	})
	if err != nil {
		return storage.Access{}, err
	}
	return storage.Access{Location: request.URL}, nil
}
