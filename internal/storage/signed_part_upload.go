package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func UploadSignedMultipartPart(ctx context.Context, signedURL string, content []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	request.ContentLength = int64(len(content))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2048))
		if readErr != nil {
			return "", fmt.Errorf("read multipart part error body: %w", readErr)
		}
		return "", fmt.Errorf("multipart part put failed status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	etag := strings.Trim(strings.TrimSpace(response.Header.Get("ETag")), "\"")
	if etag == "" {
		return "", fmt.Errorf("multipart part upload missing etag")
	}
	return etag, nil
}
