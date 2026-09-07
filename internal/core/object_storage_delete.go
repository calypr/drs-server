package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type storageTarget struct {
	provider string
	bucket   string
	key      string
	path     string
}

type s3ObjectDeleter interface {
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	DeleteObjects(context.Context, *awss3.DeleteObjectsInput, ...func(*awss3.Options)) (*awss3.DeleteObjectsOutput, error)
}

var newS3ObjectDeleter = func(ctx context.Context, cred *models.S3Credential) (s3ObjectDeleter, error) {
	cfg, err := awsConfigForStorageCredential(ctx, cred)
	if err != nil {
		return nil, err
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if strings.TrimSpace(cred.Endpoint) != "" {
			o.UsePathStyle = true
		}
	}), nil
}

func (m *ObjectManager) deleteObjectStorage(ctx context.Context, obj *models.InternalObject) error {
	targets, err := m.storageTargetsForObject(ctx, obj)
	if err != nil {
		return err
	}
	return m.deleteStorageTargets(ctx, targets)
}

func (m *ObjectManager) deleteObjectsStorage(ctx context.Context, objects []models.InternalObject) error {
	targets := make([]storageTarget, 0, len(objects))
	seen := make(map[string]struct{})
	for i := range objects {
		objectTargets, err := m.storageTargetsForObject(ctx, &objects[i])
		if err != nil {
			return err
		}
		for _, target := range objectTargets {
			key := storageTargetKey(target)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, target)
		}
	}
	return m.deleteStorageTargets(ctx, targets)
}

func (m *ObjectManager) deleteStorageTargets(ctx context.Context, targets []storageTarget) error {
	s3ByBucket := make(map[string][]string)
	for _, target := range targets {
		if target.provider == common.S3Provider {
			bucket := strings.TrimSpace(target.bucket)
			key := strings.Trim(strings.TrimSpace(target.key), "/")
			if bucket == "" || key == "" {
				continue
			}
			s3ByBucket[bucket] = append(s3ByBucket[bucket], key)
			continue
		}
		if err := m.deleteStorageTarget(ctx, target); err != nil {
			return err
		}
	}
	buckets := make([]string, 0, len(s3ByBucket))
	for bucket := range s3ByBucket {
		buckets = append(buckets, bucket)
	}
	sort.Strings(buckets)
	for _, bucket := range buckets {
		keys := dedupeSortedStrings(s3ByBucket[bucket])
		if err := m.deleteS3Objects(ctx, bucket, keys); err != nil {
			return err
		}
	}
	return nil
}

func (m *ObjectManager) storageTargetsForObject(ctx context.Context, obj *models.InternalObject) ([]storageTarget, error) {
	if obj == nil || obj.AccessMethods == nil {
		return nil, nil
	}

	targets := make([]storageTarget, 0, len(*obj.AccessMethods))
	seen := make(map[string]struct{}, len(*obj.AccessMethods))
	for _, am := range *obj.AccessMethods {
		if am.AccessUrl == nil || strings.TrimSpace(am.AccessUrl.Url) == "" {
			continue
		}

		rawURL := strings.TrimSpace(am.AccessUrl.Url)
		scopedURL := rawURL
		// Stored locations are physical replicas. Their bucket/key must remain
		// exact through deletion; project scopes are upload-time concerns.
		scopedURL = rawURL

		target, ok, err := m.storageTargetFromURL(ctx, scopedURL)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		key := storageTargetKey(target)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	return targets, nil
}

func (m *ObjectManager) storageTargetFromURL(ctx context.Context, raw string) (storageTarget, bool, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return storageTarget{}, false, fmt.Errorf("parse access url %q: %w", raw, err)
	}
	if provider := common.ProviderFromScheme(u.Scheme); provider != "" {
		bucket := strings.TrimSpace(u.Host)
		key := strings.Trim(strings.TrimSpace(u.Path), "/")
		if bucket == "" || key == "" {
			return storageTarget{}, false, nil
		}
		cred, err := m.credentialForBucket(ctx, bucket)
		if err != nil {
			return storageTarget{}, false, fmt.Errorf("lookup credential for bucket %s: %w", bucket, err)
		}
		normalizedProvider := provider
		if cred != nil {
			normalizedProvider = common.NormalizeProvider(cred.Provider, provider)
		}
		if normalizedProvider == common.FileProvider {
			if cred == nil {
				return storageTarget{}, false, fmt.Errorf("file-backed bucket %s requires credential", bucket)
			}
			root := filepath.Clean(strings.TrimSpace(cred.Endpoint))
			if root == "." || root == "" {
				root = strings.TrimPrefix(strings.TrimSpace(cred.Bucket), "/")
			}
			if root == "" {
				return storageTarget{}, false, fmt.Errorf("file-backed bucket %s missing storage root", bucket)
			}
			return storageTarget{
				provider: common.FileProvider,
				bucket:   bucket,
				key:      key,
				path:     filepath.Clean(filepath.Join(root, filepath.FromSlash(key))),
			}, true, nil
		}
		return storageTarget{provider: normalizedProvider, bucket: bucket, key: key}, true, nil
	}

	if filepath.IsAbs(raw) {
		return storageTarget{provider: common.FileProvider, path: filepath.Clean(raw)}, true, nil
	}
	return storageTarget{}, false, nil
}

func (m *ObjectManager) deleteStorageTarget(ctx context.Context, target storageTarget) error {
	switch target.provider {
	case common.FileProvider:
		if strings.TrimSpace(target.path) == "" {
			return nil
		}
		if err := os.Remove(target.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete file %s: %w", target.path, err)
		}
		return nil
	case common.S3Provider:
		return m.deleteS3Object(ctx, target.bucket, target.key)
	case common.GCSProvider:
		return m.deleteGCSObject(ctx, target.bucket, target.key)
	case common.AzureProvider:
		return m.deleteAzureObject(ctx, target.bucket, target.key)
	default:
		return fmt.Errorf("unsupported storage provider %q", target.provider)
	}
}

func (m *ObjectManager) deleteS3Object(ctx context.Context, bucket, key string) error {
	return m.deleteS3Objects(ctx, bucket, []string{key})
}

func (m *ObjectManager) deleteS3Objects(ctx context.Context, bucket string, keys []string) error {
	keys = dedupeSortedStrings(keys)
	if len(keys) == 0 {
		return nil
	}
	cred, err := m.credentialForBucket(ctx, bucket)
	if err != nil {
		return fmt.Errorf("lookup s3 credential for bucket %s: %w", bucket, err)
	}
	if cred == nil {
		return fmt.Errorf("credentials not found for bucket %s", bucket)
	}
	client, err := newS3ObjectDeleter(ctx, cred)
	if err != nil {
		return err
	}

	if len(keys) == 1 {
		_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(keys[0]),
		})
		return err
	}

	const maxS3DeleteObjects = 1000
	quiet := true
	for start := 0; start < len(keys); start += maxS3DeleteObjects {
		end := start + maxS3DeleteObjects
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]s3types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, s3types.ObjectIdentifier{Key: aws.String(key)})
		}
		out, err := client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(quiet),
			},
		})
		if err != nil {
			return err
		}
		if out != nil && len(out.Errors) > 0 {
			return fmt.Errorf("s3 bulk delete failed for bucket %s: %s", bucket, formatS3DeleteErrors(out.Errors))
		}
	}
	return nil
}

func awsConfigForStorageCredential(ctx context.Context, cred *models.S3Credential) (aws.Config, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cred.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cred.AccessKey, cred.SecretKey, "")),
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	if endpoint := strings.TrimSpace(cred.Endpoint); endpoint != "" {
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
				endpoint = "http://" + endpoint
			} else {
				endpoint = "https://" + endpoint
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}
	return cfg, nil
}

func storageTargetKey(target storageTarget) string {
	return target.provider + "\x00" + target.bucket + "\x00" + target.key + "\x00" + target.path
}

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func formatS3DeleteErrors(errors []s3types.Error) string {
	const maxErrors = 5
	parts := make([]string, 0, len(errors))
	for i, item := range errors {
		if i >= maxErrors {
			parts = append(parts, fmt.Sprintf("and %d more", len(errors)-maxErrors))
			break
		}
		key := strings.TrimSpace(aws.ToString(item.Key))
		code := strings.TrimSpace(aws.ToString(item.Code))
		message := strings.TrimSpace(aws.ToString(item.Message))
		switch {
		case key != "" && code != "" && message != "":
			parts = append(parts, fmt.Sprintf("%s: %s: %s", key, code, message))
		case key != "" && code != "":
			parts = append(parts, fmt.Sprintf("%s: %s", key, code))
		case code != "":
			parts = append(parts, code)
		default:
			parts = append(parts, "unknown delete error")
		}
	}
	return strings.Join(parts, "; ")
}

func (m *ObjectManager) deleteGCSObject(ctx context.Context, bucket, key string) error {
	cred, err := m.bucketCatalog.getS3Credential(ctx, bucket)
	if err != nil {
		return fmt.Errorf("lookup gcs credential for bucket %s: %w", bucket, err)
	}
	if cred == nil {
		return fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	secret := strings.TrimSpace(cred.SecretKey)
	var client *storage.Client
	if secret != "" && json.Valid([]byte(secret)) {
		client, err = storage.NewClient(ctx, option.WithCredentialsJSON([]byte(secret)))
	} else {
		client, err = storage.NewClient(ctx)
	}
	if err != nil {
		return fmt.Errorf("create gcs client: %w", err)
	}
	defer client.Close()

	err = client.Bucket(bucket).Object(key).Delete(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
		return nil
	}
	return err
}

func (m *ObjectManager) deleteAzureObject(ctx context.Context, bucket, key string) error {
	cred, err := m.bucketCatalog.getS3Credential(ctx, bucket)
	if err != nil {
		return fmt.Errorf("lookup azure credential for bucket %s: %w", bucket, err)
	}
	if cred == nil {
		return fmt.Errorf("credentials not found for bucket %s", bucket)
	}

	accountName := strings.TrimSpace(cred.AccessKey)
	if accountName == "" {
		accountName = azureAccountFromEndpoint(strings.TrimSpace(cred.Endpoint))
	}
	accountKey := strings.TrimSpace(cred.SecretKey)
	if accountName == "" || accountKey == "" {
		return fmt.Errorf("azure deletion requires shared key credentials for bucket %s", bucket)
	}

	shared, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return fmt.Errorf("parse azure shared key: %w", err)
	}
	client, err := azblob.NewClientWithSharedKeyCredential(azureServiceURL(accountName, cred.Endpoint), shared, nil)
	if err != nil {
		return fmt.Errorf("create azure client: %w", err)
	}

	_, err = client.DeleteBlob(ctx, bucket, key, nil)
	if err == nil {
		return nil
	}
	if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && strings.EqualFold(apiErr.ErrorCode(), "BlobNotFound") {
		return nil
	}
	return err
}

func azureServiceURL(accountName string, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep != "" {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			ep = "https://" + ep
		}
		return strings.TrimRight(ep, "/")
	}
	return "https://" + strings.TrimSpace(accountName) + ".blob.core.windows.net"
}

func azureAccountFromEndpoint(endpoint string) string {
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
	return strings.TrimSpace(parts[0])
}
