package transfers

import (
	"context"
	"fmt"
	"sync"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
)

// MultipartLifecycle owns the provider target associated with each upload ID.
type MultipartLifecycle struct {
	service  *Service
	sessions sync.Map
}

type multipartTarget struct {
	bucket string
	key    string
}

type multipartSession struct {
	target    multipartTarget
	complete  chan struct{}
	completed bool
}

func newMultipartSession(bucket, key string) *multipartSession {
	complete := make(chan struct{}, 1)
	complete <- struct{}{}
	return &multipartSession{target: multipartTarget{bucket: bucket, key: key}, complete: complete}
}

func (s *multipartSession) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.complete:
		if err := ctx.Err(); err != nil {
			s.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *multipartSession) release() {
	s.complete <- struct{}{}
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
	l.sessions.Store(uploadID, newMultipartSession(bucket, key))
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
		return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	session, err := l.session(uploadID)
	if err != nil {
		return err
	}
	if err := session.acquire(ctx); err != nil {
		return err
	}
	defer session.release()
	if session.completed {
		return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	if err := l.service.CompleteMultipartUpload(ctx, session.target.bucket, session.target.key, uploadID, parts); err != nil {
		return err
	}
	session.completed = true
	l.sessions.CompareAndDelete(uploadID, session)
	return nil
}

func (l *MultipartLifecycle) target(uploadID string) (multipartTarget, error) {
	session, err := l.session(uploadID)
	if err != nil {
		return multipartTarget{}, err
	}
	return session.target, nil
}

func (l *MultipartLifecycle) session(uploadID string) (*multipartSession, error) {
	if l == nil {
		return nil, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	target, ok := l.sessions.Load(uploadID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	return target.(*multipartSession), nil
}
