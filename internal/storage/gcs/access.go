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
	credentials storageports.CredentialLookup
	cache       sync.Map // keyed by the lookup bucket string, stores *storage.Client
}

// New constructs the GCS provider registration.
func New(credentials storageports.CredentialLookup) storageports.Registration {
	return storageports.NewRegistration(address.GCSProvider, &backend{credentials: credentials})
}

func (b *backend) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	b.cache.Delete(bucket)
}

func (b *backend) SignURL(ctx context.Context, target storageports.ObjectTarget, opts storageports.AccessOptions) (storageports.Access, error) {
	cred, err := b.credential(ctx, target.Bucket)
	if err != nil {
		return storageports.Access{}, err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	method := http.MethodGet
	if opts.Method != "" {
		method = opts.Method
	}

	location, err := b.signedURL(target.Bucket, target.Key, method, expiry, "", opts.DownloadFilename, cred)
	if err != nil {
		return storageports.Access{}, err
	}
	return storageports.Access{Location: location}, nil
}

func (b *backend) SignDownloadPart(ctx context.Context, target storageports.ObjectTarget, byteRange storageports.ByteRange, opts storageports.AccessOptions) (storageports.Access, error) {
	cred, err := b.credential(ctx, target.Bucket)
	if err != nil {
		return storageports.Access{}, err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	rangeValue := fmt.Sprintf("bytes=%d-%d", byteRange.Start, byteRange.End)
	location, err := b.signedURL(target.Bucket, target.Key, http.MethodGet, expiry, rangeValue, opts.DownloadFilename, cred)
	if err != nil {
		return storageports.Access{}, err
	}
	return storageports.Access{Location: location}, nil
}

func (b *backend) credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	cred, err := b.credentials.GetS3Credential(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", bucket)
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
