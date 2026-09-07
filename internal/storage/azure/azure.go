package azure

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type backend struct {
	credentials storage.CredentialLookup
	cache       sync.Map // keyed by bucket name, stores *azureCreds
	transport   policy.Transporter
}

type azureCreds struct {
	SharedKey        *azblob.SharedKeyCredential
	ServiceURL       string
	DeleteServiceURL string
}

// New returns the Azure storage registration. Azure intentionally does not
// expose probe or inventory capabilities; those operations are not provided
// by the Azure raw-storage backend.
func New(credentials storage.CredentialLookup) storage.Registration {
	return storage.NewRegistration("azure", &backend{credentials: credentials})
}

func (b *backend) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	b.cache.Delete(bucket)
}

func (b *backend) SignURL(ctx context.Context, target storage.ObjectTarget, opts storage.AccessOptions) (storage.Access, error) {
	creds, err := b.getCreds(ctx, target.Bucket)
	if err != nil {
		return storage.Access{}, err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	method := http.MethodGet
	if opts.Method != "" {
		method = opts.Method
	}

	signed, err := b.azureSignedURL(creds.ServiceURL, target.Bucket, target.Key, method, expiry, "", opts.DownloadFilename, creds.SharedKey)
	if err != nil {
		return storage.Access{}, err
	}
	return storage.Access{Location: signed}, nil
}

func (b *backend) SignDownloadPart(ctx context.Context, target storage.ObjectTarget, byteRange storage.ByteRange, opts storage.AccessOptions) (storage.Access, error) {
	creds, err := b.getCreds(ctx, target.Bucket)
	if err != nil {
		return storage.Access{}, err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	// Azure SAS does not encode this range. Keep computing it and passing it
	// through the signing seam to preserve the current caller contract.
	rangeStr := fmt.Sprintf("bytes=%d-%d", byteRange.Start, byteRange.End)
	signed, err := b.azureSignedURL(creds.ServiceURL, target.Bucket, target.Key, http.MethodGet, expiry, rangeStr, opts.DownloadFilename, creds.SharedKey)
	if err != nil {
		return storage.Access{}, err
	}
	return storage.Access{Location: signed}, nil
}

func (b *backend) InitMultipartUpload(_ context.Context, _ storage.ObjectTarget) (storage.UploadID, error) {
	return storage.UploadID(uuid.NewString()), nil
}

func (b *backend) getCreds(ctx context.Context, bucket string) (*azureCreds, error) {
	if value, ok := b.cache.Load(bucket); ok {
		return value.(*azureCreds), nil
	}

	cred, err := b.credentials.GetS3Credential(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	accountName := strings.TrimSpace(cred.AccessKey)
	if accountName == "" {
		accountName = b.azureAccountFromEndpoint(cred.Endpoint)
	}
	accountKey := strings.TrimSpace(cred.SecretKey)
	if accountName == "" || accountKey == "" {
		return nil, fmt.Errorf("azure signing requires shared key credentials for bucket %s", bucket)
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
	b.cache.Store(bucket, value)
	return value, nil
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
