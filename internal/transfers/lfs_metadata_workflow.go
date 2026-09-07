package transfers

import (
	"context"
	"errors"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
)

type LFSMetadataObjectPort interface {
	GetObject(context.Context, string, string) (*objects.Record, error)
	RegisterObjects(context.Context, []objects.Record) error
}

type LFSMetadataCandidateError struct {
	Err error
}

func (e *LFSMetadataCandidateError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid LFS metadata candidate"
	}
	return e.Err.Error()
}

func (e *LFSMetadataCandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type LFSMetadataStageError struct {
	Index      int
	Err        error
	MissingSHA bool
}

func (e *LFSMetadataStageError) Error() string {
	if e.MissingSHA {
		return "candidate missing canonical sha256"
	}
	if e.Err == nil {
		return "candidate is invalid"
	}
	return e.Err.Error()
}

func (e *LFSMetadataStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type LFSMetadataWorkflow struct {
	transfer   *Service
	objects    LFSMetadataObjectPort
	accounting LFSUploadAccounting
	now        func() time.Time
}

func NewLFSMetadataWorkflow(transfer *Service, objectPort LFSMetadataObjectPort, accounting LFSUploadAccounting) *LFSMetadataWorkflow {
	return &LFSMetadataWorkflow{
		transfer:   transfer,
		objects:    objectPort,
		accounting: accounting,
		now:        time.Now,
	}
}

func (w *LFSMetadataWorkflow) Stage(ctx context.Context, candidates []objects.Candidate) error {
	now := w.now().UTC()
	entries := make([]PendingMetadata, 0, len(candidates))
	for index, candidate := range candidates {
		internalObject, err := objects.CandidateToRecord(candidate, now)
		if err != nil {
			return &LFSMetadataStageError{Index: index, Err: err}
		}
		oid, ok := objects.CanonicalSHA256(internalObject.Checksums)
		if !ok {
			return &LFSMetadataStageError{Index: index, MissingSHA: true}
		}
		entries = append(entries, PendingMetadata{
			OID:       oid,
			Candidate: candidate,
			CreatedAt: now,
			ExpiresAt: now.Add(PendingMetadataTTL),
		})
	}
	return w.transfer.SavePendingLFSMeta(ctx, entries)
}

func (w *LFSMetadataWorkflow) Verify(ctx context.Context, oid string) error {
	object, err := w.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return w.recordUpload(ctx, string(object.Id))
	}
	if !faults.IsNotFoundError(err) {
		return err
	}

	pending, err := w.transfer.PopPendingLFSMeta(ctx, oid)
	if err != nil {
		return err
	}
	internalObject, err := objects.CandidateToRecord(pending.Candidate, w.now().UTC())
	if err != nil {
		return &LFSMetadataCandidateError{Err: err}
	}
	if err := w.objects.RegisterObjects(ctx, []objects.Record{internalObject}); err != nil {
		return err
	}
	return w.recordUpload(ctx, string(internalObject.Id))
}

func (w *LFSMetadataWorkflow) recordUpload(ctx context.Context, objectID string) error {
	if w.accounting == nil {
		return errors.New("file counters are not configured")
	}
	return w.accounting.RecordFileUpload(ctx, objectID)
}
