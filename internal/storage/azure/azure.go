package azure

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type backend struct {
	cache       sync.Map // keyed by lookup key, stores *azureCreds
	cacheMu     sync.Mutex
	generation  map[string]uint64
	entries     map[string]*cacheEntry
	invalidated map[string]map[string]struct{}
	transport   policy.Transporter
}

type azureCreds struct {
	SharedKey        *azblob.SharedKeyCredential
	ServiceURL       string
	DeleteServiceURL string
}

type cacheEntry struct {
	creds       *azureCreds
	fingerprint string
}

// New returns the Azure storage registration. Azure intentionally does not
// expose probe or inventory capabilities; those operations are not provided
// by the Azure raw-storage backend.
func New() storage.Registration {
	return storage.NewRegistration("azure", &backend{})
}

func (b *backend) InvalidateBucket(bucket string) {
	bucket = strings.ToLower(strings.TrimSpace(bucket))
	if bucket == "" {
		return
	}
	b.cacheMu.Lock()
	if b.generation == nil {
		b.generation = make(map[string]uint64)
	}
	if b.entries == nil {
		b.entries = make(map[string]*cacheEntry)
	}
	if b.invalidated == nil {
		b.invalidated = make(map[string]map[string]struct{})
	}
	if current := b.entries[bucket]; current != nil {
		if current.fingerprint != "" {
			if b.invalidated[bucket] == nil {
				b.invalidated[bucket] = make(map[string]struct{})
			}
			b.invalidated[bucket][current.fingerprint] = struct{}{}
		}
		delete(b.entries, bucket)
	}
	b.generation[bucket]++
	b.cache.Range(func(key, _ any) bool {
		if strings.ToLower(strings.TrimSpace(fmt.Sprint(key))) == bucket {
			b.cache.Delete(key)
		}
		return true
	})
	b.cacheMu.Unlock()
}

func (b *backend) Sign(ctx context.Context, binding storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	creds, err := b.getCreds(binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	expiry := 15 * time.Minute
	if request.ExpiresIn > 0 {
		expiry = request.ExpiresIn
	}

	method := http.MethodGet
	if request.Method != "" {
		method = request.Method
	}

	rangeStr := ""
	if request.Range != nil {
		rangeStr = fmt.Sprintf("bytes=%d-%d", request.Range.Start, request.Range.End)
	}
	signed, err := b.azureSignedURL(creds.ServiceURL, request.Target.PhysicalBucket, request.Target.Key, method, expiry, rangeStr, request.DownloadFilename, creds.SharedKey)
	if err != nil {
		return storage.SignedAccess{}, err
	}
	return storage.SignedAccess{Location: signed}, nil
}

func (b *backend) BeginMultipart(_ context.Context, _ storage.ProviderBinding, _ storage.BeginMultipartRequest) (storage.UploadID, error) {
	return storage.UploadID(uuid.NewString()), nil
}

func (b *backend) getCreds(binding storage.ProviderBinding) (*azureCreds, error) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	cacheKey := strings.ToLower(strings.TrimSpace(binding.LookupKey))
	if cacheKey == "" {
		cacheKey = strings.ToLower(strings.TrimSpace(binding.PhysicalBucket))
	}
	if b.entries == nil {
		b.entries = make(map[string]*cacheEntry)
	}
	if b.invalidated == nil {
		b.invalidated = make(map[string]map[string]struct{})
	}
	fingerprint := b.credentialFingerprint(binding.Credential)
	if current := b.entries[cacheKey]; current != nil {
		if current.fingerprint == "" || current.fingerprint == fingerprint {
			return current.creds, nil
		}
		if _, stale := b.invalidated[cacheKey][fingerprint]; stale {
			return current.creds, nil
		}
		delete(b.entries, cacheKey)
		b.cache.Delete(cacheKey)
	}
	if value, ok := b.cache.Load(cacheKey); ok {
		cached := value.(*azureCreds)
		if _, stale := b.invalidated[cacheKey][fingerprint]; !stale {
			b.entries[cacheKey] = &cacheEntry{creds: cached, fingerprint: fingerprint}
		}
		return cached, nil
	}
	var cached *azureCreds
	b.cache.Range(func(key, value any) bool {
		if strings.ToLower(strings.TrimSpace(fmt.Sprint(key))) == cacheKey {
			cached, _ = value.(*azureCreds)
			return false
		}
		return true
	})
	if cached != nil {
		if _, stale := b.invalidated[cacheKey][fingerprint]; stale {
			return cached, nil
		}
		b.entries[cacheKey] = &cacheEntry{creds: cached, fingerprint: fingerprint}
		b.cache.Store(cacheKey, cached)
		return cached, nil
	}

	cred := binding.Credential
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", binding.PhysicalBucket)
	}

	accountName := strings.TrimSpace(cred.AccessKey)
	if accountName == "" {
		accountName = b.azureAccountFromEndpoint(cred.Endpoint)
	}
	accountKey := strings.TrimSpace(cred.SecretKey)
	if accountName == "" || accountKey == "" {
		return nil, fmt.Errorf("azure signing requires shared key credentials for bucket %s", binding.PhysicalBucket)
	}

	shared, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse azure shared key: %w", err)
	}

	value := &azureCreds{
		SharedKey:        shared,
		ServiceURL:       b.azureServiceURL(accountName, cred.Endpoint),
		DeleteServiceURL: b.azureDeleteServiceURL(accountName, cred.Endpoint),
	}
	if _, stale := b.invalidated[cacheKey][fingerprint]; !stale {
		b.entries[cacheKey] = &cacheEntry{creds: value, fingerprint: fingerprint}
		b.cache.Store(cacheKey, value)
	}
	return value, nil
}

func (b *backend) credentialFingerprint(cred *buckets.Credential) string {
	if cred == nil {
		return ""
	}
	material := strings.Join([]string{cred.Provider, cred.Bucket, cred.Region, cred.AccessKey, cred.SecretKey, cred.Endpoint}, "\x00")
	hash := sha256.Sum256([]byte(material))
	return hex.EncodeToString(hash[:])
}

func (b *backend) azureSignedURL(serviceURL, bucketName, key, method string, expiry time.Duration, rangeStr, downloadName string, sharedKey *azblob.SharedKeyCredential) (string, error) {
	blobURL := b.azureBlobURL(serviceURL, bucketName, key)
	now := time.Now().UTC()
	permissions := (&sas.BlobPermissions{Read: true}).String()
	if method == http.MethodPut {
		permissions = (&sas.BlobPermissions{Add: true, Create: true, Write: true}).String()
	}

	query, err := sas.BlobSignatureValues{
		Protocol:           azureSASProtocol(serviceURL),
		StartTime:          now.Add(-5 * time.Minute),
		ExpiryTime:         now.Add(expiry),
		Permissions:        permissions,
		ContainerName:      bucketName,
		BlobName:           strings.Trim(strings.TrimSpace(key), "/"),
		ContentDisposition: storage.ContentDispositionAttachment(downloadName),
	}.SignWithSharedKey(sharedKey)
	if err != nil {
		return "", err
	}

	return blobURL + "?" + query.Encode(), nil
}

func (b *backend) azureBlockID(uploadID storage.UploadID, partNumber int32) string {
	raw := fmt.Sprintf("%s:%08d", strings.TrimSpace(string(uploadID)), partNumber)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func (b *backend) azureBlobURL(serviceURL, bucket, key string) string {
	base := strings.TrimRight(strings.TrimSpace(serviceURL), "/")
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	if cleanKey == "" {
		return base + "/" + url.PathEscape(bucket)
	}
	segments := strings.Split(cleanKey, "/")
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(segment))
	}
	return base + "/" + url.PathEscape(bucket) + "/" + strings.Join(escaped, "/")
}

func (b *backend) azureServiceURL(accountName, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep != "" {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			ep = "https://" + ep
		}
		return strings.TrimRight(ep, "/")
	}
	return "https://" + strings.TrimSpace(accountName) + ".blob.db.windows.net"
}

func (b *backend) azureDeleteServiceURL(accountName, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep != "" {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			ep = "https://" + ep
		}
		return strings.TrimRight(ep, "/")
	}
	return "https://" + strings.TrimSpace(accountName) + ".blob.core.windows.net"
}

func (b *backend) azureAccountFromEndpoint(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ""
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func azureSASProtocol(serviceURL string) sas.Protocol {
	u, err := url.Parse(strings.TrimSpace(serviceURL))
	if err == nil && strings.EqualFold(strings.TrimSpace(u.Scheme), "http") {
		return sas.ProtocolHTTPSandHTTP
	}
	return sas.ProtocolHTTPS
}
