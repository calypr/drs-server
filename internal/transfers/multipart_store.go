package transfers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage"
)

type MultipartState string

const (
	MultipartStateActive     MultipartState = "active"
	MultipartStateCompleting MultipartState = "completing"
	MultipartStateCompleted  MultipartState = "completed"
)

type MultipartAuthorization struct {
	Resources []string     `json:"resources,omitempty"`
	Methods   []string     `json:"methods,omitempty"`
	Scope     *AccessScope `json:"scope,omitempty"`
}

func (a MultipartAuthorization) Authorize(ctx context.Context) error {
	if !access.IsAuthzEnforced(ctx) {
		return nil
	}
	if a.Scope != nil {
		return access.AuthorizeScopeWrite(ctx, a.Scope.Organization, a.Scope.Project, a.Methods...)
	}
	for _, method := range a.Methods {
		if access.HasObjectMethodAccess(ctx, method, a.Resources) {
			return nil
		}
	}
	return errorapi.ErrAccessDenied
}

type MultipartSession struct {
	UploadID          string                 `json:"upload_id"`
	CompletionID      string                 `json:"completion_id"`
	Target            storage.Target         `json:"target"`
	Authorization     MultipartAuthorization `json:"authorization"`
	State             MultipartState         `json:"state"`
	CompletionToken   string                 `json:"completion_token,omitempty"`
	PartsFingerprint  string                 `json:"parts_fingerprint,omitempty"`
	CompletedLocation string                 `json:"completed_location,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type memoryMultipartSessionStore struct {
	mu       sync.Mutex
	sessions map[string]MultipartSession
}

func newMemoryMultipartSessionStore() *memoryMultipartSessionStore {
	return &memoryMultipartSessionStore{sessions: make(map[string]MultipartSession)}
}

func (s *memoryMultipartSessionStore) SaveMultipartSession(_ context.Context, session MultipartSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.UploadID]; exists {
		return fmt.Errorf("%w: multipart upload ID %s already exists", errorapi.ErrConflict, session.UploadID)
	}
	s.sessions[session.UploadID] = cloneMultipartSession(session)
	return nil
}

func (s *memoryMultipartSessionStore) GetMultipartSession(_ context.Context, uploadID string) (MultipartSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return MultipartSession{}, multipartNotFound(uploadID)
	}
	return cloneMultipartSession(session), nil
}

func (s *memoryMultipartSessionStore) ClaimMultipartCompletion(_ context.Context, uploadID, token, partsFingerprint string, now, staleBefore time.Time) (MultipartSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok {
		return MultipartSession{}, false, multipartNotFound(uploadID)
	}
	sameFingerprint := session.PartsFingerprint == "" || session.PartsFingerprint == partsFingerprint
	if sameFingerprint && (session.State == MultipartStateActive || (session.State == MultipartStateCompleting && !session.UpdatedAt.After(staleBefore))) {
		session.State = MultipartStateCompleting
		session.CompletionToken = token
		if session.PartsFingerprint == "" {
			session.PartsFingerprint = partsFingerprint
		}
		session.UpdatedAt = now
		s.sessions[uploadID] = session
		return cloneMultipartSession(session), true, nil
	}
	return cloneMultipartSession(session), false, nil
}

func (s *memoryMultipartSessionStore) ReleaseMultipartCompletion(_ context.Context, uploadID, token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if ok && session.State == MultipartStateCompleting && session.CompletionToken == token {
		session.State = MultipartStateActive
		session.CompletionToken = ""
		session.UpdatedAt = now
		s.sessions[uploadID] = session
	}
	return nil
}

func (s *memoryMultipartSessionStore) FinishMultipartCompletion(_ context.Context, uploadID, token, location string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.State != MultipartStateCompleting || session.CompletionToken != token {
		return false, nil
	}
	session.State = MultipartStateCompleted
	session.CompletionToken = ""
	session.CompletedLocation = location
	session.UpdatedAt = now
	s.sessions[uploadID] = session
	return true, nil
}

func cloneMultipartSession(session MultipartSession) MultipartSession {
	session.Authorization.Resources = append([]string(nil), session.Authorization.Resources...)
	session.Authorization.Methods = append([]string(nil), session.Authorization.Methods...)
	if session.Authorization.Scope != nil {
		scope := *session.Authorization.Scope
		session.Authorization.Scope = &scope
	}
	return session
}

func multipartNotFound(uploadID string) error {
	return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
}
