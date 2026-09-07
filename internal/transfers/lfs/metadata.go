package lfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
)

type PendingMetadata struct {
	OID       string
	Candidate objects.Candidate
	CreatedAt time.Time
	ExpiresAt time.Time
}

const PendingMetadataTTL = 20 * time.Minute

type PendingStore interface {
	SavePendingMetadata(context.Context, []PendingMetadata) error
	GetPendingMetadata(context.Context, string) (*PendingMetadata, error)
	PopPendingMetadata(context.Context, string) (*PendingMetadata, error)
}

type MetadataObjectPort interface {
	GetObject(context.Context, string, string) (*objects.Record, error)
	RegisterObjects(context.Context, []objects.Record) error
}

type MetadataCandidateError struct {
	Err error
}

func (e *MetadataCandidateError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid LFS metadata candidate"
	}
	return e.Err.Error()
}

func (e *MetadataCandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type MetadataStageError struct {
	Index      int
	Err        error
	MissingSHA bool
}

func (e *MetadataStageError) Error() string {
	if e.MissingSHA {
		return "candidate missing canonical sha256"
	}
	if e.Err == nil {
		return "candidate is invalid"
	}
	return e.Err.Error()
}

func (e *MetadataStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type MetadataWorkflow struct {
	pending    PendingStore
	objects    MetadataObjectPort
	accounting UploadAccounting
	now        func() time.Time
}

func NewMetadataWorkflow(pending PendingStore, objectPort MetadataObjectPort, accounting UploadAccounting) *MetadataWorkflow {
	return &MetadataWorkflow{
		pending:    pending,
		objects:    objectPort,
		accounting: accounting,
		now:        time.Now,
	}
}

// StagePendingMetadata normalizes one pending candidate and applies the
// default metadata lifetime before persisting it.
func (w *MetadataWorkflow) StagePendingMetadata(ctx context.Context, metadata PendingMetadata) error {
	if strings.TrimSpace(metadata.OID) == "" {
		if oid, ok := objects.CanonicalSHA256(candidateChecksums(metadata.Candidate)); ok {
			metadata.OID = oid
		} else {
			return fmt.Errorf("%w: pending LFS metadata requires an OID", faults.ErrInvalidInput)
		}
	}
	now := time.Now().UTC()
	if w != nil && w.now != nil {
		now = w.now().UTC()
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	} else {
		metadata.CreatedAt = metadata.CreatedAt.UTC()
	}
	if metadata.ExpiresAt.IsZero() {
		metadata.ExpiresAt = metadata.CreatedAt.Add(PendingMetadataTTL)
	} else {
		metadata.ExpiresAt = metadata.ExpiresAt.UTC()
	}
	if w == nil || w.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	return w.pending.SavePendingMetadata(ctx, []PendingMetadata{metadata})
}

func (w *MetadataWorkflow) Stage(ctx context.Context, candidates []objects.Candidate) error {
	now := w.now().UTC()
	entries := make([]PendingMetadata, 0, len(candidates))
	for index, candidate := range candidates {
		internalObject, err := objects.CandidateToRecord(candidate, now)
		if err != nil {
			return &MetadataStageError{Index: index, Err: err}
		}
		oid, ok := objects.CanonicalSHA256(internalObject.Checksums)
		if !ok {
			return &MetadataStageError{Index: index, MissingSHA: true}
		}
		entries = append(entries, PendingMetadata{
			OID:       oid,
			Candidate: candidate,
			CreatedAt: now,
			ExpiresAt: now.Add(PendingMetadataTTL),
		})
	}
	if w.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	return w.pending.SavePendingMetadata(ctx, entries)
}

func (w *MetadataWorkflow) Verify(ctx context.Context, oid string) error {
	object, err := w.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return w.recordUpload(ctx, string(object.Id))
	}
	if !faults.IsNotFoundError(err) {
		return err
	}

	if w.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	pending, err := w.pending.PopPendingMetadata(ctx, oid)
	if err != nil {
		return err
	}
	internalObject, err := objects.CandidateToRecord(pending.Candidate, w.now().UTC())
	if err != nil {
		return &MetadataCandidateError{Err: err}
	}
	if err := w.objects.RegisterObjects(ctx, []objects.Record{internalObject}); err != nil {
		return err
	}
	return w.recordUpload(ctx, string(internalObject.Id))
}

func (w *MetadataWorkflow) recordUpload(ctx context.Context, objectID string) error {
	if w.accounting == nil {
		return errors.New("file counters are not configured")
	}
	return w.accounting.RecordFileUpload(ctx, objectID)
}

func candidateChecksums(candidate objects.Candidate) []objects.Checksum {
	if candidate.Checksums == nil {
		return nil
	}
	return *candidate.Checksums
}
