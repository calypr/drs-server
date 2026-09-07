package transfers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/calypr/syfon/internal/storage"
)

// ErrMultipartUploadNotFound indicates that a multipart upload is not owned by this lifecycle.
var ErrMultipartUploadNotFound = errors.New("multipart upload not found")

// MultipartLifecycle owns the provider target associated with each upload ID.
type MultipartLifecycle struct {
	service  *Service
	sessions sync.Map
}

type multipartTarget struct {
	bucket string
	key    string
}

// NewMultipartLifecycle creates an isolated multipart lifecycle for a transfer service.
func NewMultipartLifecycle(service *Service) *MultipartLifecycle {
	return &MultipartLifecycle{service: service}
}

func (l *MultipartLifecycle) Begin(ctx context.Context, bucket, key string) (string, error) {
	if l == nil {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	uploadID, err := l.service.InitMultipartUpload(ctx, bucket, key)
	if err != nil {
		return "", err
	}
	l.sessions.Store(uploadID, multipartTarget{bucket: bucket, key: key})
	return uploadID, nil
}

func (l *MultipartLifecycle) SignPart(ctx context.Context, uploadID string, partNumber int32) (string, error) {
	target, err := l.target(uploadID)
	if err != nil {
		return "", err
	}
	return l.service.SignMultipartPart(ctx, target.bucket, target.key, uploadID, partNumber)
}

func (l *MultipartLifecycle) Complete(ctx context.Context, uploadID string, parts []storage.CompletedPart) error {
	if l == nil {
		return fmt.Errorf("%w: %s", ErrMultipartUploadNotFound, uploadID)
	}
	target, ok := l.sessions.LoadAndDelete(uploadID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrMultipartUploadNotFound, uploadID)
	}
	session := target.(multipartTarget)
	return l.service.CompleteMultipartUpload(ctx, session.bucket, session.key, uploadID, parts)
}

func (l *MultipartLifecycle) target(uploadID string) (multipartTarget, error) {
	if l == nil {
		return multipartTarget{}, fmt.Errorf("%w: %s", ErrMultipartUploadNotFound, uploadID)
	}
	target, ok := l.sessions.Load(uploadID)
	if !ok {
		return multipartTarget{}, fmt.Errorf("%w: %s", ErrMultipartUploadNotFound, uploadID)
	}
	return target.(multipartTarget), nil
}
