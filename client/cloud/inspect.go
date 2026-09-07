// Package cloud contains client-side data-provider inspection helpers used when
// a caller needs to talk directly to S3/GCS/Azure object storage.
package cloud

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/gcsblob"
	"gocloud.dev/blob/s3blob"
)

// ObjectParameters contains provider-agnostic object lookup settings for
// metadata inspection.
type ObjectParameters struct {
	ObjectURL       string
	S3Region        string
	S3Endpoint      string
	S3AccessKey     string
	S3SecretKey     string
	SHA256          string
	DestinationPath string
}

// ObjectInfo is the resolved object metadata returned by inspection.
type ObjectInfo struct {
	Bucket      string
	Key         string
	Path        string
	SizeBytes   int64
	MetaSHA256  string
	ETag        string
	LastModTime time.Time
}

type objectLocation struct {
	bucketURL string
	bucket    string
	key       string
	path      string
}

var (
	virtualHostedS3RE  = regexp.MustCompile(`^(.+?)\.s3(?:[.-]|$)`)
	virtualHostedGCSRE = regexp.MustCompile(`^(.+?)\.storage\.googleapis\.com$`)
	azureBlobHostRE    = regexp.MustCompile(`^([^.]+)\.blob\.core\.windows\.net$`)
)

// InspectObject inspects object metadata for configured-bucket and raw cloud
// URL workflows.
func InspectObject(ctx context.Context, in ObjectParameters) (*ObjectInfo, error) {
	if strings.TrimSpace(in.ObjectURL) == "" {
		return nil, fmt.Errorf("ObjectURL is required")
	}

	loc, err := parseObjectLocation(in.ObjectURL, in.DestinationPath, in)
	if err != nil {
		return nil, err
	}

	cloudBucket, err := openBucketForLocation(ctx, loc, in)
	if err != nil {
		return nil, fmt.Errorf("failed to open bucket via go-cloud string %s: %w", loc.bucketURL, err)
	}
	defer cloudBucket.Close()

	attrs, err := cloudBucket.Attributes(ctx, loc.key)
	if err != nil {
		return nil, fmt.Errorf("blob attributes failed (bucket=%q key=%q): %w", loc.bucketURL, loc.key, err)
	}

	metaSHA := extractSHA256FromMetadata(attrs.Metadata)
	expected := normalizeSHA256(in.SHA256)
	if expected != "" && metaSHA != "" && !strings.EqualFold(expected, metaSHA) {
		return nil, fmt.Errorf("sha256 mismatch: expected=%s head.meta=%s", expected, metaSHA)
	}

	etag := strings.Trim(strings.TrimSpace(attrs.ETag), `"`)
	return &ObjectInfo{
		Bucket:      loc.bucket,
		Key:         loc.key,
		Path:        loc.path,
		SizeBytes:   attrs.Size,
		MetaSHA256:  metaSHA,
		ETag:        etag,
		LastModTime: attrs.ModTime,
	}, nil
}

func openBucketForLocation(ctx context.Context, loc *objectLocation, in ObjectParameters) (*blob.Bucket, error) {
	if !strings.HasPrefix(loc.bucketURL, "s3://") {
		return blob.OpenBucket(ctx, loc.bucketURL)
	}

	u, err := url.Parse(loc.bucketURL)
	if err != nil {
		return nil, fmt.Errorf("parse s3 bucket URL %q: %w", loc.bucketURL, err)
	}
	bucket := strings.TrimSpace(u.Host)
	if bucket == "" {
		return nil, fmt.Errorf("missing bucket in s3 URL %q", loc.bucketURL)
	}

	q := u.Query()
	region := strings.TrimSpace(firstNonEmpty(in.S3Region, q.Get("region")))
	endpoint := strings.TrimRight(strings.TrimSpace(firstNonEmpty(in.S3Endpoint, q.Get("endpoint"))), "/")
	accessKey := strings.TrimSpace(in.S3AccessKey)
	secretKey := strings.TrimSpace(in.S3SecretKey)

	loadOpts := make([]func(*awsconfig.LoadOptions) error, 0, 2)
	if region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))
	}
	if accessKey != "" || secretKey != "" {
		if accessKey == "" || secretKey == "" {
			return nil, fmt.Errorf("both S3AccessKey and S3SecretKey are required when either is provided")
		}
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	clientOpts := make([]func(*s3.Options), 0, 1)
	if endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.UsePathStyle = true
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	client := s3.NewFromConfig(awsCfg, clientOpts...)
	return s3blob.OpenBucketV2(ctx, client, bucket, nil)
}

func parseObjectLocation(raw, destinationPath string, cfg ObjectParameters) (*objectLocation, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	withPath := func(bucketURL, bucket, key string) (*objectLocation, error) {
		worktreeName := strings.TrimSpace(destinationPath)
		if worktreeName == "" {
			worktreeName = path.Base(key)
			if worktreeName == "." || worktreeName == "/" || worktreeName == "" {
				return nil, fmt.Errorf("could not derive worktree name from key %q", key)
			}
		} else if worktreeName == "." || worktreeName == "/" {
			return nil, fmt.Errorf("invalid worktree name override %q", worktreeName)
		}
		return &objectLocation{
			bucketURL: bucketURL,
			bucket:    bucket,
			key:       key,
			path:      worktreeName,
		}, nil
	}

	switch u.Scheme {
	case "s3", "gs", "azblob":
		bucket := u.Host
		key := strings.TrimPrefix(u.Path, "/")
		if bucket == "" {
			return nil, fmt.Errorf("no bucket/container in URL: %s", raw)
		}
		if key == "" {
			return nil, fmt.Errorf("no object key/path in URL: %s", raw)
		}
		return withPath(normalizeCloudBucketURL(u, cfg), bucket, key)
	case "http", "https":
		host := u.Hostname()
		key := strings.TrimPrefix(u.Path, "/")
		if key == "" {
			return nil, fmt.Errorf("no object path in URL: %s", raw)
		}

		if m := virtualHostedS3RE.FindStringSubmatch(host); m != nil {
			bucket := m[1]
			return withPath(fmt.Sprintf("s3://%s", bucket), bucket, key)
		}
		if m := virtualHostedGCSRE.FindStringSubmatch(host); m != nil {
			bucket := m[1]
			return withPath(fmt.Sprintf("gs://%s", bucket), bucket, key)
		}
		if host == "storage.googleapis.com" {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("invalid GCS path-style URL: %s", raw)
			}
			return withPath(fmt.Sprintf("gs://%s", parts[0]), parts[0], parts[1])
		}
		if m := azureBlobHostRE.FindStringSubmatch(host); m != nil {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("invalid Azure blob URL: %s", raw)
			}
			bucketURL := fmt.Sprintf("azblob://%s?account_name=%s", parts[0], m[1])
			return withPath(bucketURL, parts[0], parts[1])
		}
		if strings.Contains(host, "s3") {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("invalid S3 path-style URL: %s", raw)
			}
			endpointHint := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
			return withPath(buildS3BucketURL(parts[0], cfg, endpointHint), parts[0], parts[1])
		}
		return nil, fmt.Errorf("unsupported http(s) cloud URL: %s", raw)
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}

func buildS3BucketURL(bucket string, cfg ObjectParameters, endpointHint string) string {
	u := url.URL{Scheme: "s3", Host: bucket}
	q := url.Values{}
	if region := strings.TrimSpace(cfg.S3Region); region != "" {
		q.Set("region", region)
	}
	if endpoint := strings.TrimRight(firstNonEmpty(endpointHint, cfg.S3Endpoint), "/"); endpoint != "" {
		q.Set("endpoint", endpoint)
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func normalizeCloudBucketURL(u *url.URL, cfg ObjectParameters) string {
	bucketURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if u.Scheme == "s3" {
		bucketURL = buildS3BucketURL(u.Host, cfg, "")
	}
	if u.RawQuery == "" {
		return bucketURL
	}

	parsed, err := url.Parse(bucketURL)
	if err != nil {
		return bucketURL
	}
	q := parsed.Query()
	for k, vs := range u.Query() {
		q.Del(k)
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

var sha256HexRe = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)

func normalizeSHA256(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.ToLower(s), "sha256:")
	s = strings.TrimSpace(s)
	if s == "" || !sha256HexRe.MatchString(s) {
		return ""
	}
	return strings.ToLower(s)
}

func extractSHA256FromMetadata(md map[string]string) string {
	if md == nil {
		return ""
	}
	for _, k := range []string{"sha256", "checksum-sha256", "content-sha256", "oid-sha256", "git-lfs-sha256"} {
		if v, ok := md[k]; ok {
			if n := normalizeSHA256(v); n != "" {
				return n
			}
		}
	}
	for _, v := range md {
		if n := normalizeSHA256(v); n != "" {
			return n
		}
	}
	return ""
}
