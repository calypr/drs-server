package s3

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/signer"
)

type S3Signer struct {
	db    db.CredentialStore
	cache sync.Map // keyed by bucket name, stores *s3Clients
}

type s3Clients struct {
	client    *s3.Client
	presigner *s3.PresignClient
}

func NewS3Signer(db db.CredentialStore) *S3Signer {
	return &S3Signer{db: db}
}

func (s *S3Signer) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	s.cache.Delete(bucket)
}

func (s *S3Signer) SignURL(ctx context.Context, bucket, key string, opts signer.SignOptions) (string, error) {
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return "", err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	if opts.Method == http.MethodPut {
		req, err := clients.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}, func(o *s3.PresignOptions) {
			o.Expires = expiry
		})
		if err != nil {
			return "", err
		}
		return req.URL, nil
	}

	req, err := clients.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: responseContentDisposition(opts.DownloadFilename),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (s *S3Signer) SignDownloadPart(ctx context.Context, bucket, key string, start, end int64, opts signer.SignOptions) (string, error) {
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return "", err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	rangeStr := fmt.Sprintf("bytes=%d-%d", start, end)
	req, err := clients.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(bucket),
		Key:                        aws.String(key),
		Range:                      aws.String(rangeStr),
		ResponseContentDisposition: responseContentDisposition(opts.DownloadFilename),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func responseContentDisposition(name string) *string {
	disposition := common.ContentDispositionAttachment(name)
	if disposition == "" {
		return nil
	}
	return aws.String(disposition)
}

func (s *S3Signer) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return "", err
	}

	out, err := clients.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to init s3 multipart upload: %w", err)
	}
	return aws.ToString(out.UploadId), nil
}

func (s *S3Signer) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return "", err
	}

	expiry := 15 * time.Minute // Default for part signing

	req, err := clients.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign s3 multipart part: %w", err)
	}
	return req.URL, nil
}

func (s *S3Signer) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []signer.MultipartPart) error {
	clients, err := s.getClients(ctx, bucket)
	if err != nil {
		return err
	}

	completedParts := make([]types.CompletedPart, 0, len(parts))
	for _, p := range parts {
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		})
	}

	_, err = clients.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete s3 multipart upload: %w", err)
	}
	return nil
}

func (s *S3Signer) getClients(ctx context.Context, bucket string) (*s3Clients, error) {
	if val, ok := s.cache.Load(bucket); ok {
		return val.(*s3Clients), nil
	}

	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials for bucket %s: %w", bucket, err)
	}
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cred.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cred.AccessKey, cred.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %w", err)
	}

	if cred.Endpoint != "" {
		endpoint := cred.Endpoint
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
				endpoint = "http://" + endpoint
			} else {
				endpoint = "https://" + endpoint
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if cred.Endpoint != "" {
			o.UsePathStyle = true
		}
	})

	cls := &s3Clients{
		client:    client,
		presigner: s3.NewPresignClient(client),
	}
	s.cache.Store(bucket, cls)
	return cls, nil
}
