package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/signer"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

type GCSSigner struct {
	db    db.CredentialStore
	cache sync.Map // keyed by bucket name, stores *storage.Client
}

func NewGCSSigner(db db.CredentialStore) *GCSSigner {
	return &GCSSigner{db: db}
}

func (s *GCSSigner) InvalidateBucket(bucket string) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return
	}
	s.cache.Delete(bucket)
}

func (s *GCSSigner) SignURL(ctx context.Context, bucket, key string, opts signer.SignOptions) (string, error) {
	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	method := http.MethodGet
	if opts.Method != "" {
		method = opts.Method
	}

	return s.gcsSignedURL(bucket, key, method, expiry, "", opts.DownloadFilename, cred)
}

func (s *GCSSigner) SignDownloadPart(ctx context.Context, bucket, key string, start, end int64, opts signer.SignOptions) (string, error) {
	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	expiry := 15 * time.Minute
	if opts.ExpiresIn > 0 {
		expiry = opts.ExpiresIn
	}

	rangeStr := fmt.Sprintf("bytes=%d-%d", start, end)
	return s.gcsSignedURL(bucket, key, http.MethodGet, expiry, rangeStr, opts.DownloadFilename, cred)
}

func (s *GCSSigner) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	return uuid.NewString(), nil
}

func (s *GCSSigner) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	partKey := signer.MultipartPartObjectKey(key, uploadID, partNumber)
	return s.gcsSignedURL(bucket, partKey, http.MethodPut, 15*time.Minute, "", "", cred)
}

func (s *GCSSigner) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []signer.MultipartPart) error {
	client, err := s.getClient(ctx, bucket)
	if err != nil {
		return err
	}

	partList := signer.NormalizedMultipartParts(parts)
	partKeys := make([]string, 0, len(partList))
	for _, p := range partList {
		partKeys = append(partKeys, signer.MultipartPartObjectKey(key, uploadID, p.PartNumber))
	}

	tempKeys, err := s.composeGCSObjects(ctx, client, bucket, strings.Trim(strings.TrimSpace(key), "/"), uploadID, partKeys)
	if err != nil {
		return err
	}

	for _, k := range append(partKeys, tempKeys...) {
		if err := client.Bucket(bucket).Object(k).Delete(ctx); err != nil {
			return fmt.Errorf("delete multipart component %s: %w", k, err)
		}
	}
	return nil
}

func (s *GCSSigner) gcsSignedURL(bucket, key, method string, expiry time.Duration, rangeStr string, downloadName string, cred *models.S3Credential) (string, error) {
	if endpointURL, ok := gcsEndpointObjectURL(cred, bucket, key, method, downloadName); ok {
		return endpointURL, nil
	}

	googleAccessID := s.gcsGoogleAccessID(cred)
	privateKey := s.gcsPrivateKey(cred)
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
	if rangeStr != "" {
		opts.Headers = append(opts.Headers, "Range:"+rangeStr)
	}
	if disposition := common.ContentDispositionAttachment(downloadName); disposition != "" {
		opts.QueryParameters = make(url.Values)
		opts.QueryParameters.Set("response-content-disposition", disposition)
	}
	return storage.SignedURL(bucket, key, opts)
}

func gcsEndpointObjectURL(cred *models.S3Credential, bucket string, key string, method string, downloadName string) (string, bool) {
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
	base.RawQuery = ""
	base.Fragment = ""

	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPut:
		builtPath := strings.Join([]string{prefix, "upload", "storage", "v1", "b", bucketEscaped, "o"}, "/")
		builtPath = strings.ReplaceAll(builtPath, "//", "/")
		if !strings.HasPrefix(builtPath, "/") {
			builtPath = "/" + builtPath
		}
		if len(builtPath) > 1 && (builtPath[1] == '/' || builtPath[1] == '\\') {
			return "", false
		}
		base.Path = builtPath
		q := base.Query()
		q.Set("uploadType", "media")
		q.Set("name", cleanKey)
		base.RawQuery = q.Encode()
		return base.String(), true
	default:
		builtPath := strings.Join([]string{prefix, "storage", "v1", "b", bucketEscaped, "o", keyEscaped}, "/")
		builtPath = strings.ReplaceAll(builtPath, "//", "/")
		if !strings.HasPrefix(builtPath, "/") {
			builtPath = "/" + builtPath
		}
		if len(builtPath) > 1 && (builtPath[1] == '/' || builtPath[1] == '\\') {
			return "", false
		}
		base.Path = builtPath
		q := base.Query()
		q.Set("alt", "media")
		if disposition := common.ContentDispositionAttachment(downloadName); disposition != "" {
			q.Set("response-content-disposition", disposition)
		}
		base.RawQuery = q.Encode()
		return base.String(), true
	}
}

func (s *GCSSigner) gcsGoogleAccessID(cred *models.S3Credential) string {
	googleAccessID := strings.TrimSpace(cred.AccessKey)
	privateKey := strings.TrimSpace(cred.SecretKey)
	var keyJSON struct {
		ClientEmail string `json:"client_email"`
	}
	if json.Unmarshal([]byte(privateKey), &keyJSON) == nil && strings.TrimSpace(keyJSON.ClientEmail) != "" {
		return strings.TrimSpace(keyJSON.ClientEmail)
	}
	return googleAccessID
}

func (s *GCSSigner) gcsPrivateKey(cred *models.S3Credential) string {
	privateKey := strings.TrimSpace(cred.SecretKey)
	var keyJSON struct {
		PrivateKey string `json:"private_key"`
	}
	if json.Unmarshal([]byte(privateKey), &keyJSON) == nil && strings.TrimSpace(keyJSON.PrivateKey) != "" {
		return strings.TrimSpace(keyJSON.PrivateKey)
	}
	return privateKey
}

func (s *GCSSigner) getClient(ctx context.Context, bucket string) (*storage.Client, error) {
	if val, ok := s.cache.Load(bucket); ok {
		return val.(*storage.Client), nil
	}

	cred, err := s.db.GetS3Credential(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	secret := strings.TrimSpace(cred.SecretKey)
	var client *storage.Client
	if secret != "" && json.Valid([]byte(secret)) {
		client, err = storage.NewClient(ctx, option.WithCredentialsJSON([]byte(secret)))
	} else {
		client, err = storage.NewClient(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	s.cache.Store(bucket, client)
	return client, nil
}

func (s *GCSSigner) composeGCSObjects(ctx context.Context, client *storage.Client, bucket string, destinationKey string, uploadID string, partKeys []string) ([]string, error) {
	if len(partKeys) == 0 {
		return nil, fmt.Errorf("multipart complete requires at least one part")
	}
	current := append([]string(nil), partKeys...)
	tempKeys := []string{}
	round := 0
	for len(current) > 32 {
		next := []string{}
		for i := 0; i < len(current); i += 32 {
			end := i + 32
			if end > len(current) {
				end = len(current)
			}
			tmp := path.Join(".syfon-multipart", strings.TrimSpace(uploadID), strings.Trim(strings.TrimSpace(destinationKey), "/"), "compose", fmt.Sprintf("%d-%d", round, i/32))
			if err := s.composeGCSBatch(ctx, client, bucket, tmp, current[i:end]); err != nil {
				return tempKeys, err
			}
			tempKeys = append(tempKeys, tmp)
			next = append(next, tmp)
		}
		current = next
		round++
	}
	if err := s.composeGCSBatch(ctx, client, bucket, destinationKey, current); err != nil {
		return tempKeys, err
	}
	return tempKeys, nil
}

func (s *GCSSigner) composeGCSBatch(ctx context.Context, client *storage.Client, bucket string, dst string, src []string) error {
	dstObj := client.Bucket(bucket).Object(dst)
	srcObjs := make([]*storage.ObjectHandle, 0, len(src))
	for _, k := range src {
		srcObjs = append(srcObjs, client.Bucket(bucket).Object(k))
	}
	if _, err := dstObj.ComposerFrom(srcObjs...).Run(ctx); err != nil {
		return fmt.Errorf("failed gcs compose for %s: %w", dst, err)
	}
	return nil
}
