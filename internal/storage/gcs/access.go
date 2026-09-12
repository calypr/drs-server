package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"

	"github.com/calypr/syfon/internal/buckets"
	storageports "github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

// backend is the GCS implementation of storage's private complete backend
// contract. GCS intentionally has no Probe or Inventory methods: those are
// optional capabilities and are not implemented by this provider.
type backend struct {
	cache       sync.Map // keyed by canonical lookup bucket, stores *storage.Client
	cacheMu     sync.Mutex
	clients     map[string]*clientEntry
	allClients  map[*clientEntry]struct{}
	closed      bool
	closeOnce   sync.Once
	closeErr    error
	closeErrors []error
	activeCond  *sync.Cond
}

type clientEntry struct {
	client     *storage.Client
	credential credentialIdentity
	users      int
	retired    bool
	closed     bool
}

type credentialIdentity struct {
	present  bool
	provider string
	bucket   string
	region   string
	access   string
	secret   string
	endpoint string
}

func credentialIdentityOf(cred *buckets.Credential) credentialIdentity {
	if cred == nil {
		return credentialIdentity{}
	}
	return credentialIdentity{
		present:  true,
		provider: cred.Provider,
		bucket:   cred.Bucket,
		region:   cred.Region,
		access:   cred.AccessKey,
		secret:   cred.SecretKey,
		endpoint: cred.Endpoint,
	}
}

// New constructs the GCS provider registration.
func New() storageports.Registration {
	return storageports.NewRegistration(address.GCSProvider, &backend{})
}

func (b *backend) InvalidateBucket(bucket string) {
	bucket = canonicalCacheKey(bucket)
	if bucket == "" {
		return
	}
	b.cacheMu.Lock()
	if b.clients == nil {
		b.clients = make(map[string]*clientEntry)
	}
	if b.allClients == nil {
		b.allClients = make(map[*clientEntry]struct{})
	}
	if current := b.clients[bucket]; current != nil {
		current.retired = true
		delete(b.clients, bucket)
		if current.users == 0 {
			b.closeEntryLocked(current)
		}
	}
	b.cache.Range(func(key, _ any) bool {
		if canonicalCacheKey(fmt.Sprint(key)) == bucket {
			b.cache.Delete(key)
		}
		return true
	})
	b.cacheMu.Unlock()
}

func canonicalCacheKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (b *backend) Sign(ctx context.Context, binding storageports.ProviderBinding, request storageports.SignRequest) (storageports.SignedAccess, error) {
	cred, err := b.credential(binding)
	if err != nil {
		return storageports.SignedAccess{}, err
	}

	expiry := 15 * time.Minute
	if request.ExpiresIn > 0 {
		expiry = request.ExpiresIn
	}

	method := http.MethodGet
	if request.Method != "" {
		method = request.Method
	}

	rangeValue := ""
	if request.Range != nil {
		rangeValue = fmt.Sprintf("bytes=%d-%d", request.Range.Start, request.Range.End)
	}
	location, err := b.signedURL(request.Target.PhysicalBucket, request.Target.Key, method, expiry, rangeValue, request.DownloadFilename, cred)
	if err != nil {
		return storageports.SignedAccess{}, err
	}
	return storageports.SignedAccess{Location: location}, nil
}

func (b *backend) credential(binding storageports.ProviderBinding) (*buckets.Credential, error) {
	cred := binding.Credential
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", binding.PhysicalBucket)
	}
	return cred, nil
}

func (b *backend) signedURL(bucket, key, method string, expiry time.Duration, rangeValue, downloadName string, cred *buckets.Credential) (string, error) {
	if endpointURL, ok := endpointObjectURL(cred, bucket, key, method, downloadName); ok {
		return endpointURL, nil
	}

	googleAccessID := googleAccessID(cred)
	privateKey := privateKey(cred)
	if googleAccessID == "" || privateKey == "" {
		return "", fmt.Errorf("gcs signing requires service account credentials (access_key=client_email, secret_key=private_key or JSON key)")
	}

	opts := &storage.SignedURLOptions{
		GoogleAccessID: googleAccessID,
		PrivateKey:     []byte(privateKey),
		Method:         method,
		Expires:        time.Now().Add(expiry),
		Scheme:         storage.SigningSchemeV4,
	}
	if rangeValue != "" {
		opts.Headers = append(opts.Headers, "Range:"+rangeValue)
	}
	if disposition := storageports.ContentDispositionAttachment(downloadName); disposition != "" {
		opts.QueryParameters = make(url.Values)
		opts.QueryParameters.Set("response-content-disposition", disposition)
	}
	return storage.SignedURL(bucket, key, opts)
}

func endpointObjectURL(cred *buckets.Credential, bucket, key, method, downloadName string) (string, bool) {
	if cred == nil {
		return "", false
	}
	endpoint := strings.TrimSpace(cred.Endpoint)
	if endpoint == "" {
		return "", false
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	base, err := url.Parse(endpoint)
	if err != nil || strings.TrimSpace(base.Host) == "" {
		return "", false
	}
	bucketEscaped := url.PathEscape(strings.TrimSpace(bucket))
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	keyEscaped := url.PathEscape(cleanKey)
	prefix := strings.TrimRight(strings.TrimSpace(base.Path), "/")
	escapedPrefix := strings.TrimRight(strings.TrimSpace(base.EscapedPath()), "/")
	base.RawQuery = ""
	base.Fragment = ""
	setPath := func(decodedParts, escapedParts []string) bool {
		builtPath := strings.Join(decodedParts, "/")
		escapedPath := strings.Join(escapedParts, "/")
		if !strings.HasPrefix(builtPath, "/") {
			builtPath = "/" + builtPath
		}
		if !strings.HasPrefix(escapedPath, "/") {
			escapedPath = "/" + escapedPath
		}
		if len(builtPath) > 1 && (builtPath[1] == '/' || builtPath[1] == '\\') {
			return false
		}
		base.Path = builtPath
		base.RawPath = escapedPath
		return true
	}

	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPut:
		if !setPath(
			[]string{prefix, "upload", "storage", "v1", "b", strings.TrimSpace(bucket), "o"},
			[]string{escapedPrefix, "upload", "storage", "v1", "b", bucketEscaped, "o"},
		) {
			return "", false
		}
		query := base.Query()
		query.Set("uploadType", "media")
		query.Set("name", cleanKey)
		base.RawQuery = query.Encode()
		return base.String(), true
	default:
		if !setPath(
			[]string{prefix, "storage", "v1", "b", strings.TrimSpace(bucket), "o", cleanKey},
			[]string{escapedPrefix, "storage", "v1", "b", bucketEscaped, "o", keyEscaped},
		) {
			return "", false
		}
		query := base.Query()
		query.Set("alt", "media")
		if disposition := storageports.ContentDispositionAttachment(downloadName); disposition != "" {
			query.Set("response-content-disposition", disposition)
		}
		base.RawQuery = query.Encode()
		return base.String(), true
	}
}

func googleAccessID(cred *buckets.Credential) string {
	accessID := strings.TrimSpace(cred.AccessKey)
	secret := strings.TrimSpace(cred.SecretKey)
	var keyJSON struct {
		ClientEmail string `json:"client_email"`
	}
	if json.Unmarshal([]byte(secret), &keyJSON) == nil && strings.TrimSpace(keyJSON.ClientEmail) != "" {
		return strings.TrimSpace(keyJSON.ClientEmail)
	}
	return accessID
}

func privateKey(cred *buckets.Credential) string {
	secret := strings.TrimSpace(cred.SecretKey)
	var keyJSON struct {
		PrivateKey string `json:"private_key"`
	}
	if json.Unmarshal([]byte(secret), &keyJSON) == nil && strings.TrimSpace(keyJSON.PrivateKey) != "" {
		return strings.TrimSpace(keyJSON.PrivateKey)
	}
	return secret
}
