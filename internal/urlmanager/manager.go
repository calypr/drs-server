package urlmanager

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer"
)

// Manager is the unified implementation of UrlManager.
// It delegates to cloud-specific Signers resolved by provider metadata.
type Manager struct {
	database        db.CredentialStore
	signing         config.SigningConfig
	defaultProvider string
	signers         map[string]signer.Signer
}

type bucketInvalidatingSigner interface {
	InvalidateBucket(bucket string)
}

func NewManager(database db.CredentialStore, signing config.SigningConfig) *Manager {
	return &Manager{
		database:        database,
		signing:         signing,
		defaultProvider: common.S3Provider,
		signers:         make(map[string]signer.Signer),
	}
}

func (m *Manager) RegisterSigner(p string, s signer.Signer) {
	m.signers[p] = s
}

func (m *Manager) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	for _, s := range m.signers {
		invalidator, ok := s.(bucketInvalidatingSigner)
		if !ok {
			continue
		}
		invalidator.InvalidateBucket(bucket)
	}
}

func (m *Manager) SignURL(ctx context.Context, accessId string, urlStr string, opts SignOptions) (string, error) {
	bucketName, key, p, err := m.resolve(ctx, accessId, urlStr)
	if err != nil {
		return "", err
	}

	s, ok := m.signers[p]
	if !ok {
		return "", fmt.Errorf("no signer registered for provider: %s", p)
	}

	return s.SignURL(ctx, bucketName, key, opts)
}

func (m *Manager) SignUploadURL(ctx context.Context, accessId string, urlStr string, opts SignOptions) (string, error) {
	if strings.TrimSpace(opts.Method) == "" {
		opts.Method = http.MethodPut
	}
	bucketName, key, p, err := m.resolve(ctx, accessId, urlStr)
	if err != nil {
		return "", err
	}

	s, ok := m.signers[p]
	if !ok {
		return "", fmt.Errorf("no signer registered for provider: %s", p)
	}

	return s.SignURL(ctx, bucketName, key, opts)
}

func (m *Manager) SignDownloadPart(ctx context.Context, accessId string, urlStr string, start int64, end int64, opts SignOptions) (string, error) {
	bucketName, key, p, err := m.resolve(ctx, accessId, urlStr)
	if err != nil {
		return "", err
	}

	s, ok := m.signers[p]
	if !ok {
		return "", fmt.Errorf("no signer registered for provider: %s", p)
	}

	return s.SignDownloadPart(ctx, bucketName, key, start, end, opts)
}

func (m *Manager) InitMultipartUpload(ctx context.Context, bucketName string, key string) (string, error) {
	p, err := m.resolveProviderForBucket(ctx, bucketName)
	if err != nil {
		return "", err
	}
	s, ok := m.signers[p]
	if !ok {
		return "", fmt.Errorf("no signer registered for provider: %s", p)
	}
	return s.InitMultipartUpload(ctx, bucketName, key)
}

func (m *Manager) SignMultipartPart(ctx context.Context, bucketName string, key string, uploadId string, partNumber int32) (string, error) {
	p, err := m.resolveProviderForBucket(ctx, bucketName)
	if err != nil {
		return "", err
	}
	s, ok := m.signers[p]
	if !ok {
		return "", fmt.Errorf("no signer registered for provider: %s", p)
	}
	return s.SignMultipartPart(ctx, bucketName, key, uploadId, partNumber)
}

func (m *Manager) CompleteMultipartUpload(ctx context.Context, bucketName string, key string, uploadId string, parts []MultipartPart) error {
	p, err := m.resolveProviderForBucket(ctx, bucketName)
	if err != nil {
		return err
	}
	s, ok := m.signers[p]
	if !ok {
		return fmt.Errorf("no signer registered for provider: %s", p)
	}
	return s.CompleteMultipartUpload(ctx, bucketName, key, uploadId, parts)
}

func (m *Manager) resolve(ctx context.Context, accessId string, urlStr string) (bucket string, key string, p string, err error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse url: %w", err)
	}

	bucket = u.Host
	key = strings.TrimPrefix(u.Path, "/")

	// Resolve provider
	candidates := []string{bucket, accessId}
	for _, c := range candidates {
		candidate := strings.TrimSpace(c)
		if candidate == "" {
			continue
		}
		if res, err := m.resolveProviderForBucket(ctx, candidate); err == nil {
			return bucket, key, res, nil
		}
	}

	// Fallback to URL scheme
	schemeProvider := common.ProviderFromScheme(u.Scheme)
	p = common.NormalizeProvider(schemeProvider, m.defaultProvider)
	return bucket, key, p, nil
}

func (m *Manager) resolveProviderForBucket(ctx context.Context, bucket string) (string, error) {
	cred, err := m.credentialForBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	return common.NormalizeProvider(cred.Provider, m.defaultProvider), nil
}

func (m *Manager) credentialForBucket(ctx context.Context, bucket string) (*models.S3Credential, error) {
	key := strings.TrimSpace(bucket)
	if key == "" {
		return nil, fmt.Errorf("bucket is required")
	}

	cred, err := m.database.GetS3Credential(ctx, key)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential not found")
	}
	return cred, nil
}
