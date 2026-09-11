package transfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/request"
)

// DoUpload performs a presigned PUT request and returns ETag when available.
func DoUpload(ctx context.Context, client request.HTTPDoer, urlStr string, body io.Reader, size int64) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(urlStr))
	if err == nil && (parsed.Scheme == "" || strings.ToLower(parsed.Scheme) == "file") {
		dstPath := parsed.Path
		if dstPath == "" {
			dstPath = urlStr
		}
		if dstPath == "" {
			return "", fmt.Errorf("invalid file upload url: %s", urlStr)
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return "", fmt.Errorf("create upload target dir: %w", err)
		}
		f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return "", fmt.Errorf("open upload target file: %w", err)
		}
		defer f.Close()
		_, err = io.Copy(f, body)
		return "", err
	}

	method := http.MethodPut
	if parsed != nil && useGCSJSONMediaUpload(parsed) {
		method = http.MethodPost
	}

	skipAuth := common.IsCloudPresignedURL(urlStr)
	ctx, cancel := context.WithTimeout(ctx, common.DataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}
	if skipAuth {
		request.SkipAuth(req)
	}
	if method == http.MethodPut && parsed != nil && needsAzureBlobTypeHeader(parsed) {
		req.Header.Set("x-ms-blob-type", "BlockBlob")
	}
	if size > 0 {
		req.ContentLength = size
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload to %s failed: %w", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", common.ResponseBodyError(resp, fmt.Sprintf("upload to %s failed", urlStr))
	}

	return strings.Trim(resp.Header.Get("ETag"), `"`), nil
}

// GenericDownload performs GET (optionally ranged) against a signed URL.
func GenericDownload(ctx context.Context, client request.HTTPDoer, signedURL string, rangeStart, rangeEnd *int64) (*http.Response, error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(signedURL))
	if parseErr == nil && (parsed.Scheme == "" || strings.ToLower(parsed.Scheme) == "file") {
		srcPath := parsed.Path
		if srcPath == "" {
			srcPath = signedURL
		}
		if srcPath == "" {
			return nil, fmt.Errorf("invalid file download url: %s", signedURL)
		}
		f, err := os.Open(srcPath)
		if err != nil {
			return nil, fmt.Errorf("open download source file: %w", err)
		}
		stat, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("stat download source file: %w", err)
		}

		reader := io.ReadCloser(f)
		status := http.StatusOK
		contentLength := stat.Size()
		if rangeStart != nil {
			start := *rangeStart
			if start < 0 {
				start = 0
			}
			end := stat.Size() - 1
			if rangeEnd != nil && *rangeEnd >= 0 && *rangeEnd < end {
				end = *rangeEnd
			}
			if start > stat.Size() {
				start = stat.Size()
			}
			if end < start-1 {
				end = start - 1
			}
			length := int64(0)
			if end >= start {
				length = end - start + 1
			}
			reader = io.NopCloser(io.NewSectionReader(f, start, length))
			status = http.StatusPartialContent
			contentLength = length
		}

		return &http.Response{
			StatusCode:    status,
			Body:          reader,
			ContentLength: contentLength,
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, err
	}
	if rangeStart != nil {
		rangeHeader := "bytes=" + strconv.FormatInt(*rangeStart, 10) + "-"
		if rangeEnd != nil {
			rangeHeader += strconv.FormatInt(*rangeEnd, 10)
		}
		req.Header.Set("Range", rangeHeader)
	}

	if common.IsCloudPresignedURL(signedURL) {
		request.SkipAuth(req)
	}

	return client.Do(req)
}

func needsAzureBlobTypeHeader(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return false
	}
	q := parsed.Query()
	if strings.TrimSpace(q.Get("comp")) != "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(q.Get("sr")), "b") {
		return false
	}
	return strings.TrimSpace(q.Get("sig")) != "" && strings.TrimSpace(q.Get("sv")) != ""
}

func useGCSJSONMediaUpload(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	if strings.TrimSpace(parsed.Query().Get("uploadType")) != "media" {
		return false
	}
	if strings.TrimSpace(parsed.Query().Get("name")) == "" {
		return false
	}
	return strings.Contains(parsed.EscapedPath(), "/upload/storage/v1/b/")
}
