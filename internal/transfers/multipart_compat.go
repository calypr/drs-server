package transfers

// Deprecated: legacy tests and downstream adapters should use Service's
// BeginMultipart, SignMultipartPart, and CompleteMultipart methods directly.
// This adapter carries no provider policy and is retained only for the
// compatibility callers that have not yet moved to the generated boundary.
import (
	"context"
	"fmt"
	"sync"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
)

type MultipartLifecycle struct {
	service  *Service
	mu       sync.Mutex
	sessions map[string]storage.Target
}

func NewMultipartLifecycle(service *Service) *MultipartLifecycle {
	return &MultipartLifecycle{service: service, sessions: map[string]storage.Target{}}
}

func (l *MultipartLifecycle) Begin(ctx context.Context, bucket, key string) (string, error) {
	target := storage.Target{PhysicalBucket: bucket, LookupKey: bucket, Key: key, LookupCandidates: []string{bucket}}
	id, err := l.service.beginMultipartTarget(ctx, target)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	l.sessions[string(id)] = target
	l.mu.Unlock()
	l.service.multipartMu.Lock()
	if l.service.multipartSessions == nil {
		l.service.multipartSessions = map[string]*multipartSession{}
	}
	l.service.multipartSessions[string(id)] = newMultipartSession(target)
	l.service.multipartMu.Unlock()
	return string(id), nil
}

func (l *MultipartLifecycle) SignPart(ctx context.Context, id string, part int32) (string, error) {
	target, err := l.target(id)
	if err != nil {
		return "", err
	}
	signed, err := l.service.signMultipartPartTarget(ctx, target, storage.UploadID(id), part)
	if err != nil {
		return "", err
	}
	return signed.Location, nil
}

func (l *MultipartLifecycle) Complete(ctx context.Context, id string, parts []storage.CompletedPart) error {
	if _, err := l.target(id); err != nil {
		return err
	}
	if _, err := l.service.multipartSession(id); err != nil {
		return err
	}
	domainParts := make([]CompletedPart, len(parts))
	for i, part := range parts {
		domainParts[i] = CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
	}
	if err := l.service.CompleteMultipart(ctx, id, domainParts); err != nil {
		return err
	}
	l.mu.Lock()
	delete(l.sessions, id)
	l.mu.Unlock()
	return nil
}

func (l *MultipartLifecycle) target(id string) (storage.Target, error) {
	l.mu.Lock()
	target, ok := l.sessions[id]
	l.mu.Unlock()
	if !ok {
		return storage.Target{}, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, id)
	}
	return target, nil
}
