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

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/signer"
	"github.com/google/uuid"
)

type AzureSigner struct {
	db    db.CredentialStore
	cache sync.Map // keyed by bucket name, stores *azureCreds
}

type azureCreds struct {
	SharedKey  *azblob.SharedKeyCredential
	ServiceURL string
}

func NewAzureSigner(db db.CredentialStore) *AzureSigner {
	return &AzureSigner{db: db}
}

func (s *AzureSigner) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	s.cache.Delete(bucket)
}

func (s *AzureSigner) SignURL(ctx context.Context, bucket, key string, opts signer.SignOptions) (string, error) {
	creds, err := s.getCreds(ctx, bucket)
	if err != nil {
		return "", err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	method := http.MethodGet
	if opts.Method != "" {
		method = opts.Method
	}

	return s.azureSignedURL(creds.ServiceURL, bucket, key, method, expiry, "", opts.DownloadFilename, creds.SharedKey)
}

func (s *AzureSigner) SignDownloadPart(ctx context.Context, bucket, key string, start, end int64, opts signer.SignOptions) (string, error) {
	creds, err := s.getCreds(ctx, bucket)
	if err != nil {
		return "", err
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	rangeStr := fmt.Sprintf("bytes=%d-%d", start, end)
	return s.azureSignedURL(creds.ServiceURL, bucket, key, http.MethodGet, expiry, rangeStr, opts.DownloadFilename, creds.SharedKey)
}

func (s *AzureSigner) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	return uuid.NewString(), nil
}

func (s *AzureSigner) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	creds, err := s.getCreds(ctx, bucket)
	if err != nil {
		return "", err
	}

	signed, err := s.azureSignedURL(creds.ServiceURL, bucket, key, http.MethodPut, 15*time.Minute, "", "", creds.SharedKey)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(signed)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("comp", "block")
	query.Set("blockid", s.azureBlockID(uploadID, partNumber))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (s *AzureSigner) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []signer.MultipartPart) error {
	creds, err := s.getCreds(ctx, bucket)
	if err != nil {
		return err
	}

	blobURL := s.azureBlobURL(creds.ServiceURL, bucket, key)
	client, err := blockblob.NewClientWithSharedKeyCredential(blobURL, creds.SharedKey, nil)
	if err != nil {
		return fmt.Errorf("failed to create azure block blob client: %w", err)
	}

	partList := signer.NormalizedMultipartParts(parts)
	blockIDs := make([]string, 0, len(partList))
	for _, p := range partList {
		blockIDs = append(blockIDs, s.azureBlockID(uploadID, p.PartNumber))
	}

	if _, err := client.CommitBlockList(ctx, blockIDs, nil); err != nil {
		return fmt.Errorf("failed to complete azure multipart upload: %w", err)
	}
	return nil
}

func (s *AzureSigner) getCreds(ctx context.Context, bucket string) (*azureCreds, error) {
	if val, ok := s.cache.Load(bucket); ok {
		return val.(*azureCreds), nil
	}

	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	accountName := strings.TrimSpace(cred.AccessKey)
	if accountName == "" {
		accountName = s.azureAccountFromEndpoint(cred.Endpoint)
	}
	accountKey := strings.TrimSpace(cred.SecretKey)

	if accountName == "" || accountKey == "" {
		return nil, fmt.Errorf("azure signing requires shared key credentials for bucket %s", bucket)
	}

	shared, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse azure shared key: %w", err)
	}

	c := &azureCreds{
		SharedKey:  shared,
		ServiceURL: s.azureServiceURL(accountName, cred.Endpoint),
	}
	s.cache.Store(bucket, c)
	return c, nil
}

func (s *AzureSigner) azureSignedURL(serviceURL string, bucketName string, key string, method string, expiry time.Duration, rangeStr string, downloadName string, sharedKey *azblob.SharedKeyCredential) (string, error) {
	blobURL := s.azureBlobURL(serviceURL, bucketName, key)
	now := time.Now().UTC()
	perm := (&sas.BlobPermissions{Read: true}).String()
	if method == http.MethodPut {
		perm = (&sas.BlobPermissions{Add: true, Create: true, Write: true}).String()
	}

	qp, err := sas.BlobSignatureValues{
		Protocol:           azureSASProtocol(serviceURL),
		StartTime:          now.Add(-5 * time.Minute),
		ExpiryTime:         now.Add(expiry),
		Permissions:        perm,
		ContainerName:      bucketName,
		BlobName:           strings.Trim(strings.TrimSpace(key), "/"),
		ContentDisposition: common.ContentDispositionAttachment(downloadName),
	}.SignWithSharedKey(sharedKey)
	if err != nil {
		return "", err
	}

	return blobURL + "?" + qp.Encode(), nil
}

func (s *AzureSigner) azureBlockID(uploadID string, partNumber int32) string {
	raw := fmt.Sprintf("%s:%08d", strings.TrimSpace(uploadID), partNumber)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func (s *AzureSigner) azureBlobURL(serviceURL string, bucket string, key string) string {
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

func (s *AzureSigner) azureServiceURL(accountName string, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep != "" {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			ep = "https://" + ep
		}
		return strings.TrimRight(ep, "/")
	}
	return "https://" + strings.TrimSpace(accountName) + ".blob.db.windows.net"
}

func (s *AzureSigner) azureAccountFromEndpoint(endpoint string) string {
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
