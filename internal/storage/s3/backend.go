package s3

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const defaultExpiry = 15 * time.Minute

// backend owns all S3-specific capabilities. The process cache intentionally
// remains keyed by the lookup bucket string, matching the previous signer.
// Request-scoped policy caches stay above this package.
type backend struct {
	credentials storage.CredentialLookup
	cache       sync.Map // bucket string -> *clients
	limiter     *probeLimiter
}

type clients struct {
	client    s3Client
	presigner s3Presigner
}

type s3Client interface {
	CreateMultipartUpload(context.Context, *awss3.CreateMultipartUploadInput, ...func(*awss3.Options)) (*awss3.CreateMultipartUploadOutput, error)
	CompleteMultipartUpload(context.Context, *awss3.CompleteMultipartUploadInput, ...func(*awss3.Options)) (*awss3.CompleteMultipartUploadOutput, error)
	HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	DeleteObjects(context.Context, *awss3.DeleteObjectsInput, ...func(*awss3.Options)) (*awss3.DeleteObjectsOutput, error)
}

type s3Presigner interface {
	PresignGetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignPutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignUploadPart(context.Context, *awss3.UploadPartInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// New constructs the S3 registration expected by storage.NewManager.
func New(credentials storage.CredentialLookup) storage.Registration {
	return storage.NewRegistration(address.S3Provider, &backend{
		credentials: credentials,
		limiter:     newProbeLimiterFromEnv(),
	})
}

func newBackend(credentials storage.CredentialLookup) *backend {
	return &backend{credentials: credentials, limiter: newProbeLimiterFromEnv()}
}

func (s *backend) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	s.cache.Delete(bucket)
}

func (s *backend) getClients(ctx context.Context, bucket string) (*clients, error) {
	if value, ok := s.cache.Load(bucket); ok {
		return value.(*clients), nil
	}
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials lookup is required")
	}
	cred, err := s.credentials.GetS3Credential(ctx, bucket)
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

	endpoint := strings.TrimSpace(cred.Endpoint)
	if endpoint != "" {
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
				endpoint = "http://" + endpoint
			} else {
				endpoint = "https://" + endpoint
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}

	client := awss3.NewFromConfig(cfg, func(options *awss3.Options) {
		if endpoint != "" {
			options.UsePathStyle = true
		}
	})
	result := &clients{client: client, presigner: awss3.NewPresignClient(client)}
	s.cache.Store(bucket, result)
	return result, nil
}

func expiry(options storage.AccessOptions) time.Duration {
	if options.ExpiresIn > 0 {
		return options.ExpiresIn
	}
	return defaultExpiry
}

func responseContentDisposition(name string) *string {
	disposition := storage.ContentDispositionAttachment(name)
	if disposition == "" {
		return nil
	}
	return aws.String(disposition)
}

func methodIsPut(options storage.AccessOptions) bool {
	return options.Method == http.MethodPut
}
