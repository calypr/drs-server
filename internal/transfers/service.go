package transfers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type AccountingMode string

const (
	AccountingNone                AccountingMode = "none"
	AccountingDownloadBeforeEvent AccountingMode = "download_before_event"
	AccountingEventOnly           AccountingMode = "event_only"
)

// ByteRange is the inclusive byte range requested by a transfer operation.
// Storage providers receive the translated storage.ByteRange only inside the
// transfer service boundary.
type ByteRange struct {
	Start int64
	End   int64
}

type DownloadRequest struct {
	ObjectID   string
	AccessID   string
	ExpiresIn  time.Duration
	Range      *ByteRange
	Accounting AccountingMode
}

type DownloadResult struct {
	URL       string
	SourceURL string
	Target    storage.Target
	Object    *drs.DrsObject
}

type UploadRequest struct {
	ObjectID  string
	Key       string
	Scope     *AccessScope
	ExpiresIn time.Duration
}

type UploadResult struct {
	URL      string
	Target   storage.Target
	Existing bool
	ObjectID string
	Err      error
}

type Service struct {
	objects           ObjectPort
	storage           StoragePort
	fileCounters      usage.FileCounterRecorder
	scopes            ScopeReader
	credentials       CredentialReader
	events            EventRecorder
	now               func() time.Time
	signingExpiry     time.Duration
	multipartMu       sync.Mutex
	multipartSessions map[string]*multipartSession
}

const defaultSigningExpiry = 15 * time.Minute

func NewService(deps Dependencies) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	expires := deps.DefaultSigningExpiry
	if expires <= 0 {
		expires = defaultSigningExpiry
	}
	return &Service{objects: deps.Objects, storage: deps.Storage, fileCounters: deps.FileCounters, scopes: deps.Scopes, credentials: deps.Credentials, events: deps.Events, now: now, signingExpiry: expires}
}

func (s *Service) Download(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	if s == nil || (s.objects == nil && strings.TrimSpace(req.ObjectID) != "") || s.storage == nil {
		return DownloadResult{}, fmt.Errorf("transfer service is not configured")
	}
	obj, err := s.objects.GetObject(ctx, strings.TrimSpace(req.ObjectID), "read")
	if err != nil {
		return DownloadResult{}, err
	}
	sourceURL := accessURLForID(obj, req.AccessID)
	if strings.TrimSpace(req.AccessID) == "" && sourceURL == "" {
		sourceURL = firstSupportedAccessURL(obj)
	}
	if sourceURL == "" {
		return DownloadResult{}, errorapi.ErrObjectLocationUnavailable
	}
	target, err := s.resolveDownloadTarget(ctx, obj, sourceURL)
	if err != nil {
		return DownloadResult{}, err
	}
	expires := req.ExpiresIn
	if expires <= 0 {
		expires = s.signingExpiry
	}
	filename := ""
	if obj.Name != nil {
		filename = objects.CleanToBasename(strings.TrimSpace(*obj.Name))
	}
	var byteRange *storage.ByteRange
	var rangeStart, rangeEnd *int64
	if req.Range != nil {
		byteRange = &storage.ByteRange{Start: req.Range.Start, End: req.Range.End}
		start, end := req.Range.Start, req.Range.End
		rangeStart, rangeEnd = &start, &end
	}
	signed, err := s.sign(ctx, storage.SignRequest{Target: target, Method: http.MethodGet, ExpiresIn: expires, DownloadFilename: filename, Range: byteRange})
	if err != nil {
		return DownloadResult{}, err
	}
	result := DownloadResult{URL: signed.Location, SourceURL: sourceURL, Target: target, Object: obj}
	if req.Accounting == AccountingDownloadBeforeEvent {
		if s.fileCounters != nil {
			if err := s.fileCounters.RecordFileDownload(ctx, obj.Id); err != nil {
				return DownloadResult{}, err
			}
		}
	}
	if (req.Accounting == AccountingDownloadBeforeEvent || req.Accounting == AccountingEventOnly) && s.events != nil {
		if err := s.recordAccessIssued(ctx, AccessRequest{Object: obj, Target: target, AccessID: req.AccessID, Direction: usage.ProviderTransferDirectionDownload, StorageURL: sourceURL, RangeStart: rangeStart, RangeEnd: rangeEnd}); err != nil {
			return DownloadResult{}, err
		}
	}
	return result, nil
}

func (s *Service) UploadURL(ctx context.Context, req UploadRequest) (UploadResult, error) {
	if s == nil || s.objects == nil || s.storage == nil {
		return UploadResult{}, fmt.Errorf("transfer service is not configured")
	}
	objectID := strings.TrimSpace(req.ObjectID)
	organization, project := "", ""
	if req.Scope != nil {
		organization = req.Scope.Organization
		project = req.Scope.Project
	}
	var obj *drs.DrsObject
	var err error
	if s.objects != nil {
		obj, err = s.objects.GetObject(ctx, objectID, "update")
	} else {
		err = errorapi.ErrObjectNotFound
	}
	existing := err == nil
	if err != nil && !isNotFound(err) {
		return UploadResult{}, err
	}
	if objectID == "" {
		existing = false
	}
	var target storage.Target
	if existing {
		if strings.TrimSpace(organization) != "" {
			key := strings.Trim(strings.TrimSpace(req.Key), "/")
			if key == "" && obj != nil {
				if sha, ok := objects.CanonicalSHA256(obj.Checksums); ok {
					key = sha
				}
			}
			target, err = s.resolveScopedTarget(ctx, organization, project, key)
		} else {
			canonical, resolveErr := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: obj, Key: req.Key, PreferChecksum: true})
			if resolveErr == nil {
				target = storageTargetFromCanonical(canonical.URL, canonical)
			}
			err = resolveErr
		}
	} else {
		key := strings.Trim(strings.TrimSpace(req.Key), "/")
		if key == "" {
			key = objectID
		}
		target, err = s.resolveScopedTarget(ctx, organization, project, key)
	}
	if err != nil {
		return UploadResult{}, err
	}
	expires := req.ExpiresIn
	if expires <= 0 {
		expires = s.signingExpiry
	}
	signed, err := s.sign(ctx, storage.SignRequest{Target: target, Method: http.MethodPut, ExpiresIn: expires})
	if err != nil {
		return UploadResult{}, err
	}
	if existing {
		if err := s.recordAccessIssued(ctx, AccessRequest{Object: obj, Target: target, Scope: req.Scope, Direction: usage.ProviderTransferDirectionUpload, StorageURL: target.OriginalURL}); err != nil {
			return UploadResult{}, err
		}
	}
	return UploadResult{URL: signed.Location, Target: target, Existing: existing, ObjectID: objectID}, nil
}

func (s *Service) UploadBulk(ctx context.Context, requests []UploadRequest) []UploadResult {
	results := make([]UploadResult, len(requests))
	for i, req := range requests {
		result, err := s.UploadURL(ctx, req)
		result.ObjectID = strings.TrimSpace(req.ObjectID)
		result.Err = err
		results[i] = result
	}
	return results
}

func (s *Service) recordAccessIssued(ctx context.Context, req AccessRequest) error {
	if req.Object == nil {
		return nil
	}
	if s.events == nil {
		return fmt.Errorf("transfer event recorder is not configured")
	}
	event := eventFromObject(ctx, req)
	if s.now != nil {
		event.EventTime = s.now().UTC()
		event.EventID = usage.EventID(event)
		event.AccessGrantID = usage.GrantID(event)
	}
	return s.events.RecordTransferAttributionEvents(ctx, []usage.Event{event})
}

func (s *Service) sign(ctx context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	if s == nil || s.storage == nil {
		return storage.SignedAccess{}, fmt.Errorf("storage access is not configured")
	}
	return s.storage.Sign(ctx, request)
}

func isNotFound(err error) bool {
	return errors.Is(err, errorapi.ErrNotFound) || errors.Is(err, errorapi.ErrObjectNotFound)
}
