package upload

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	internalapi "github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type uploaderStub struct {
	resolveFunc func(context.Context, string, string, common.FileMetadata, string) (string, error)
	uploadFunc  func(context.Context, string, io.Reader, int64) error

	lastResolve struct {
		guid     string
		fileName string
		metadata common.FileMetadata
		bucket   string
	}
	lastUpload struct {
		url  string
		size int64
		body string
	}
}

func (u *uploaderStub) Name() string                    { return "uploader-stub" }
func (u *uploaderStub) Logger() transfer.TransferLogger { return transfer.NoOpLogger{} }

func (u *uploaderStub) ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	u.lastResolve.guid = guid
	u.lastResolve.fileName = filename
	u.lastResolve.metadata = metadata
	u.lastResolve.bucket = bucket
	if u.resolveFunc != nil {
		return u.resolveFunc(ctx, guid, filename, metadata, bucket)
	}
	return "https://upload.example/signed", nil
}

func (u *uploaderStub) Upload(ctx context.Context, signedURL string, body io.Reader, size int64) error {
	u.lastUpload.url = signedURL
	u.lastUpload.size = size
	if body != nil {
		data, _ := io.ReadAll(body)
		u.lastUpload.body = string(data)
	}
	if u.uploadFunc != nil {
		return u.uploadFunc(ctx, signedURL, strings.NewReader(u.lastUpload.body), size)
	}
	return nil
}

func (u *uploaderStub) MultipartInit(context.Context, string) (string, error) {
	return "upload-id", nil
}

func (u *uploaderStub) MultipartPart(context.Context, string, string, int, io.Reader) (string, error) {
	return "etag", nil
}

func (u *uploaderStub) MultipartComplete(context.Context, string, string, []transfer.MultipartPart) error {
	return nil
}

func (u *uploaderStub) InitMultipartUpload(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}
func (u *uploaderStub) GetMultipartUploadURL(context.Context, string, string, int32, string) (string, error) {
	return "", nil
}
func (u *uploaderStub) CompleteMultipartUpload(context.Context, string, string, []internalapi.InternalMultipartPart, string) error {
	return nil
}
func (u *uploaderStub) UploadPart(context.Context, string, io.Reader, int64) (string, error) {
	return "", nil
}
func (u *uploaderStub) DeleteFile(context.Context, string) (string, error) { return "", nil }
func (u *uploaderStub) CanonicalObjectURL(signedURL, bucketHint, fallbackDID string) (string, error) {
	return signedURL, nil
}

type spyLogger struct {
	transfer.NoOpLogger
	succeeded []string
	failed    []string
}

func (s *spyLogger) Succeeded(filePath, guid string) {
	s.succeeded = append(s.succeeded, filePath+"|"+guid)
}

func (s *spyLogger) Failed(filePath, filename string, metadata common.FileMetadata, guid string, retryCount int, multipart bool) {
	s.failed = append(s.failed, filePath+"|"+filename+"|"+guid)
}

func createTempFileWithData(t *testing.T, data string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "upload-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp returned error: %v", err)
	}
	if _, err := file.WriteString(data); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	return file
}
