package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const defaultExpiry = 15 * time.Minute

// backend owns all S3-specific capabilities. The process cache intentionally
// remains keyed by the lookup bucket string, matching the previous signer.
// Request-scoped policy caches stay above this package.
type backend struct {
	cache       sync.Map // bucket string -> *clients
	cacheMu     sync.Mutex
	generation  map[string]uint64
	entries     map[string]*cacheEntry
	invalidated map[string]map[string]struct{}
	limiter     *probeLimiter
}

type clients struct {
	client    s3Client
	presigner s3Presigner
}

type cacheEntry struct {
	clients     *clients
	fingerprint string
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
func New() storage.Registration {
	return storage.NewRegistration(address.S3Provider, &backend{
		limiter: newProbeLimiterFromEnv(),
	})
}

func (s *backend) InvalidateBucket(bucket string) {
	bucket = strings.ToLower(strings.TrimSpace(bucket))
	if bucket == "" {
		return
	}
	s.cacheMu.Lock()
	if s.generation == nil {
		s.generation = make(map[string]uint64)
	}
	if s.entries == nil {
		s.entries = make(map[string]*cacheEntry)
	}
	if s.invalidated == nil {
		s.invalidated = make(map[string]map[string]struct{})
	}
	if current := s.entries[bucket]; current != nil {
		if current.fingerprint != "" {
			if s.invalidated[bucket] == nil {
				s.invalidated[bucket] = make(map[string]struct{})
			}
			s.invalidated[bucket][current.fingerprint] = struct{}{}
		}
		delete(s.entries, bucket)
	}
	s.generation[bucket]++
	s.cache.Range(func(key, _ any) bool {
		if strings.ToLower(strings.TrimSpace(fmt.Sprint(key))) == bucket {
			s.cache.Delete(key)
		}
		return true
	})
	s.cacheMu.Unlock()
}

func (s *backend) getClients(ctx context.Context, binding storage.ProviderBinding) (*clients, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	cacheKey := strings.ToLower(strings.TrimSpace(binding.LookupKey))
	if cacheKey == "" {
		cacheKey = strings.ToLower(strings.TrimSpace(binding.PhysicalBucket))
	}
	if s.entries == nil {
		s.entries = make(map[string]*cacheEntry)
	}
	if s.invalidated == nil {
		s.invalidated = make(map[string]map[string]struct{})
	}
	fingerprint := s.credentialFingerprint(binding.Credential)
	if current := s.entries[cacheKey]; current != nil {
		if current.fingerprint == "" || current.fingerprint == fingerprint {
			return current.clients, nil
		}
		if _, stale := s.invalidated[cacheKey][fingerprint]; stale {
			return current.clients, nil
		}
		delete(s.entries, cacheKey)
		s.cache.Delete(cacheKey)
	}
	if value, ok := s.cache.Load(cacheKey); ok {
		cached := value.(*clients)
		if _, stale := s.invalidated[cacheKey][fingerprint]; !stale {
			s.entries[cacheKey] = &cacheEntry{clients: cached, fingerprint: fingerprint}
		}
		return cached, nil
	}
	var cached *clients
	s.cache.Range(func(key, value any) bool {
		if strings.ToLower(strings.TrimSpace(fmt.Sprint(key))) == cacheKey {
			cached, _ = value.(*clients)
			return false
		}
		return true
	})
	if cached != nil {
		if _, stale := s.invalidated[cacheKey][fingerprint]; stale {
			return cached, nil
		}
		s.entries[cacheKey] = &cacheEntry{clients: cached, fingerprint: fingerprint}
		s.cache.Store(cacheKey, cached)
		return cached, nil
	}
	cred := binding.Credential
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", binding.PhysicalBucket)
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
	if _, stale := s.invalidated[cacheKey][fingerprint]; !stale {
		s.entries[cacheKey] = &cacheEntry{clients: result, fingerprint: fingerprint}
		s.cache.Store(cacheKey, result)
	}
	return result, nil
}

func (s *backend) credentialFingerprint(cred *buckets.Credential) string {
	if cred == nil {
		return ""
	}
	material := strings.Join([]string{cred.Provider, cred.Bucket, cred.Region, cred.AccessKey, cred.SecretKey, cred.Endpoint}, "\x00")
	hash := sha256.Sum256([]byte(material))
	return hex.EncodeToString(hash[:])
}

func expiry(expiresIn time.Duration) time.Duration {
	if expiresIn > 0 {
		return expiresIn
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
