package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type uploadCheckpointPhase string

const (
	uploadCheckpointUploading  uploadCheckpointPhase = "uploading"
	uploadCheckpointCompleting uploadCheckpointPhase = "completing"
	uploadCheckpointCompleted  uploadCheckpointPhase = "completed"
)

type uploaderResumeState struct {
	SourcePath            string                   `json:"source_path"`
	ObjectKey             string                   `json:"object_key"`
	GUID                  string                   `json:"guid"`
	Bucket                string                   `json:"bucket"`
	FileSize              int64                    `json:"file_size"`
	FileModUnixNano       int64                    `json:"file_mod_unix_nano"`
	ChunkSize             int64                    `json:"chunk_size"`
	UploadID              string                   `json:"upload_id"`
	RoutingFingerprint    string                   `json:"routing_fingerprint"`
	Phase                 uploadCheckpointPhase    `json:"phase"`
	Completed             map[int]string           `json:"completed"`
	CompletionParts       []transfer.MultipartPart `json:"completion_parts,omitempty"`
	CompletionFingerprint string                   `json:"completion_fingerprint,omitempty"`
	CompletedLocation     string                   `json:"completed_location"`
}

type GenericUploader struct {
	Backend transfer.MultipartBackend
}

type uploadURLResolver interface {
	ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error)
}

type multipartInitWithMetadataResolver interface {
	InitMultipartUploadWithMetadata(ctx context.Context, guid, filename, bucket string, metadata common.FileMetadata) (uploadID string, key string, err error)
}

type multipartLocationCompleter interface {
	MultipartCompleteWithLocation(context.Context, string, string, []transfer.MultipartPart) (string, error)
}

type multipartCompletionReplaySafety interface {
	MultipartCompletionReplaySafe(context.Context, string, string, []transfer.MultipartPart) bool
}

var errMultipartCompletionRecoveryRequired = errors.New("multipart completion requires recovery")
var errMultipartCompletionUncertain = errors.New("multipart completion outcome is uncertain")

type canonicalUploadAuthorization struct {
	Organization string   `json:"organization"`
	Projects     []string `json:"projects,omitempty"`
}

func uploadRoutingFingerprint(metadata common.FileMetadata) string {
	organizationProjects := make(map[string]map[string]struct{}, len(metadata.Authorizations))
	for rawOrganization, rawProjects := range metadata.Authorizations {
		organization := strings.TrimSpace(rawOrganization)
		if organization == "" {
			continue
		}
		projectSet := organizationProjects[organization]
		if projectSet == nil {
			projectSet = make(map[string]struct{}, len(rawProjects))
			organizationProjects[organization] = projectSet
		}
		for _, rawProject := range rawProjects {
			project := strings.TrimSpace(rawProject)
			if project != "" {
				projectSet[project] = struct{}{}
			}
		}
	}
	organizations := make([]canonicalUploadAuthorization, 0, len(organizationProjects))
	for organization, projectSet := range organizationProjects {
		projects := make([]string, 0, len(projectSet))
		for project := range projectSet {
			projects = append(projects, project)
		}
		sort.Strings(projects)
		organizations = append(organizations, canonicalUploadAuthorization{
			Organization: organization,
			Projects:     projects,
		})
	}
	sort.Slice(organizations, func(i, j int) bool {
		return organizations[i].Organization < organizations[j].Organization
	})
	encoded, _ := json.Marshal(organizations)
	digest := sha256.Sum256(append([]byte("syfon-upload-routing-v1:"), encoded...))
	return fmt.Sprintf("%x", digest[:])
}

func effectiveObjectKey(req transfer.TransferRequest) string {
	if key := strings.TrimSpace(req.ObjectKey); key != "" {
		return key
	}
	return strings.TrimSpace(req.GUID)
}

func (u *GenericUploader) Upload(ctx context.Context, req transfer.TransferRequest) error {
	_, err := u.UploadWithLocation(ctx, req)
	return err
}

// UploadWithLocation returns the completed multipart target when the backend supplies it.
func (u *GenericUploader) UploadWithLocation(ctx context.Context, req transfer.TransferRequest) (string, error) {
	file, err := os.Open(req.SourcePath)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}

	if req.ForceMultipart || stat.Size() >= common.FileSizeLimit {
		return u.uploadMultipart(ctx, req, file, stat.Size())
	}
	return "", u.uploadSingle(ctx, req, file, stat.Size())
}

func (u *GenericUploader) uploadSingle(ctx context.Context, req transfer.TransferRequest, file *os.File, size int64) error {
	objectKey := effectiveObjectKey(req)
	uploadTarget := objectKey
	if uploader, ok := u.Backend.(uploadURLResolver); ok {
		presignedURL, err := uploader.ResolveUploadURL(ctx, req.GUID, objectKey, req.Metadata, req.Bucket)
		if err != nil {
			return err
		}
		uploadTarget = presignedURL
	}
	strategy := transfer.DefaultBackoff()
	reader := io.Reader(file)
	var progressReader *progressReader
	if progress := common.GetProgress(ctx); progress != nil {
		progressReader = newProgressReader(file, progress, common.GetOid(ctx), size, nil)
		reader = progressReader
	}
	if err := transfer.RetryAction(ctx, u.Backend.Logger(), strategy, common.MaxRetryCount, func() error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if progressReader != nil {
			progressReader.ResetForRetry()
		}
		err := u.Backend.Upload(ctx, uploadTarget, reader, size)
		if err != nil {
			if isProgressCallbackError(err) {
				return transfer.NonRetryable(err)
			}
			return err
		}
		if progressReader != nil {
			if finalizeErr := progressReader.Complete(); finalizeErr != nil {
				return transfer.NonRetryable(finalizeErr)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if common.GetProgress(ctx) == nil {
		emitProgress(ctx, size, size)
	}
	return nil
}

func (u *GenericUploader) uploadMultipart(ctx context.Context, req transfer.TransferRequest, file *os.File, fileSize int64) (string, error) {
	logger := u.Backend.Logger()
	chunkSize := OptimalChunkSize(fileSize)
	checkpointPath, err := CheckpointPath(req.SourcePath, req.GUID)
	if err != nil {
		return "", err
	}

	state, loaded := u.loadState(checkpointPath)
	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat upload source: %w", err)
	}
	objectKey := effectiveObjectKey(req)

	if loaded && u.matches(state, req, stat, chunkSize) {
		switch state.Phase {
		case uploadCheckpointCompleted:
			_, err := completionParts(state)
			if err != nil {
				return "", err
			}
			tracker := newMultipartProgressTracker(ctx, common.GetOid(ctx), fileSize)
			tracker.committed = fileSize
			return u.finishMultipart(checkpointPath, state, tracker)
		case uploadCheckpointCompleting:
			parts, err := completionParts(state)
			if err != nil {
				return "", err
			}
			replaySafe, ok := u.Backend.(multipartCompletionReplaySafety)
			if !ok || !replaySafe.MultipartCompletionReplaySafe(ctx, objectKey, state.UploadID, parts) {
				return "", errMultipartCompletionRecoveryRequired
			}
			location, completeErr := u.completeMultipart(ctx, objectKey, checkpointPath, state, parts)
			if completeErr != nil {
				return location, completeErr
			}
			tracker := newMultipartProgressTracker(ctx, common.GetOid(ctx), fileSize)
			tracker.committed = fileSize
			return u.finishMultipart(checkpointPath, state, tracker)
		case uploadCheckpointUploading:
		default:
			loaded = false
		}
	}
	if loaded && state != nil && state.Phase == uploadCheckpointCompleting && !u.matches(state, req, stat, chunkSize) {
		return "", errMultipartCompletionRecoveryRequired
	}

	if !loaded || !u.matches(state, req, stat, chunkSize) {
		var (
			uploadID string
			err      error
		)
		if backend, ok := u.Backend.(multipartInitWithMetadataResolver); ok {
			uploadID, _, err = backend.InitMultipartUploadWithMetadata(ctx, req.GUID, objectKey, req.Bucket, req.Metadata)
		} else {
			uploadID, err = u.Backend.MultipartInit(ctx, objectKey)
		}
		if err != nil {
			return "", err
		}
		state = &uploaderResumeState{
			SourcePath:         req.SourcePath,
			ObjectKey:          objectKey,
			GUID:               req.GUID,
			Bucket:             req.Bucket,
			FileSize:           fileSize,
			FileModUnixNano:    stat.ModTime().UnixNano(),
			ChunkSize:          chunkSize,
			UploadID:           uploadID,
			RoutingFingerprint: uploadRoutingFingerprint(req.Metadata),
			Phase:              uploadCheckpointUploading,
			Completed:          map[int]string{},
		}
		if err := u.saveState(checkpointPath, state); err != nil {
			return "", fmt.Errorf("persist multipart checkpoint: %w", err)
		}
	}

	numChunks := int((fileSize + chunkSize - 1) / chunkSize)
	chunks := make(chan int, numChunks)
	for i := 1; i <= numChunks; i++ {
		if _, ok := state.Completed[i]; !ok {
			chunks <- i
		}
	}
	close(chunks)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		uploadErr error
	)
	tracker := newMultipartProgressTracker(ctx, common.GetOid(ctx), fileSize)
	tracker.committed = completedMultipartBytes(state, fileSize, chunkSize)

	for i := 0; i < common.MaxConcurrentUploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for partNum := range chunks {
				offset := int64(partNum-1) * chunkSize
				partSize := chunkSize
				if offset+partSize > fileSize {
					partSize = fileSize - offset
				}

				strategy := transfer.DefaultBackoff()
				err := transfer.RetryAction(ctx, logger, strategy, common.MaxRetryCount, func() error {
					tracker.ResetPart(partNum)
					section := io.NewSectionReader(file, offset, partSize)
					partReader := newMultipartPartProgressReader(section, tracker, partNum, partSize)
					etag, retryErr := u.Backend.MultipartPart(ctx, objectKey, state.UploadID, partNum, partReader)
					if retryErr != nil {
						if isProgressCallbackError(retryErr) {
							return transfer.NonRetryable(retryErr)
						}
						return retryErr
					}
					if flushErr := partReader.FlushPendingProgress(); flushErr != nil {
						return transfer.NonRetryable(flushErr)
					}

					mu.Lock()
					state.Completed[partNum] = etag
					saveErr := u.saveState(checkpointPath, state)
					mu.Unlock()
					if saveErr != nil {
						return fmt.Errorf("persist multipart checkpoint: %w", saveErr)
					}
					if progressErr := tracker.CompletePart(partNum, partSize); progressErr != nil {
						return transfer.NonRetryable(progressErr)
					}
					return nil
				})
				if err != nil {
					mu.Lock()
					uploadErr = err
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()

	if uploadErr != nil {
		return "", fmt.Errorf("multipart upload failed: %w", uploadErr)
	}

	parts := make([]transfer.MultipartPart, 0, len(state.Completed))
	for num, etag := range state.Completed {
		parts = append(parts, transfer.MultipartPart{PartNumber: int32(num), ETag: etag})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	state.Phase = uploadCheckpointCompleting
	state.CompletionParts = append([]transfer.MultipartPart(nil), parts...)
	state.CompletionFingerprint = uploadPartsFingerprint(parts)
	if err := u.saveState(checkpointPath, state); err != nil {
		return "", fmt.Errorf("persist multipart completion intent: %w", err)
	}

	location, err := u.completeMultipart(ctx, objectKey, checkpointPath, state, parts)
	if err != nil {
		return location, err
	}
	return u.finishMultipart(checkpointPath, state, tracker)
}

func (u *GenericUploader) completeMultipart(ctx context.Context, objectKey, checkpointPath string, state *uploaderResumeState, parts []transfer.MultipartPart) (string, error) {
	location := ""
	var err error
	if backend, ok := u.Backend.(multipartLocationCompleter); ok {
		location, err = backend.MultipartCompleteWithLocation(ctx, objectKey, state.UploadID, parts)
	} else {
		err = u.Backend.MultipartComplete(ctx, objectKey, state.UploadID, parts)
	}
	if err != nil {
		return "", err
	}

	state.Phase = uploadCheckpointCompleted
	state.CompletedLocation = location
	if err := u.saveState(checkpointPath, state); err != nil {
		return location, fmt.Errorf("%w: persist terminal multipart checkpoint: %v", errMultipartCompletionUncertain, err)
	}
	return location, nil
}

func (u *GenericUploader) finishMultipart(checkpointPath string, state *uploaderResumeState, tracker *multipartProgressTracker) (string, error) {
	if err := tracker.CompleteUpload(); err != nil {
		return state.CompletedLocation, transfer.NonRetryable(err)
	}

	if err := removeMultipartCheckpoint(checkpointPath); err != nil && !os.IsNotExist(err) {
		return state.CompletedLocation, fmt.Errorf("remove multipart checkpoint: %w", err)
	}
	return state.CompletedLocation, nil
}

func (u *GenericUploader) loadState(path string) (*uploaderResumeState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var st uploaderResumeState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, false
	}
	if st.Completed == nil {
		st.Completed = map[int]string{}
	}
	return &st, true
}

func (u *GenericUploader) saveState(path string, state *uploaderResumeState) error {
	return uploadCheckpointWriter(path, state)
}

func writeUploadCheckpoint(path string, state *uploaderResumeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal upload checkpoint: %w", err)
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".multipart-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create upload checkpoint: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod upload checkpoint: %w", err)
	}
	written, err := temp.Write(data)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("write upload checkpoint: %w", err)
	}
	if written != len(data) {
		_ = temp.Close()
		return fmt.Errorf("write upload checkpoint: %w", io.ErrShortWrite)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync upload checkpoint: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close upload checkpoint: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish upload checkpoint: %w", err)
	}
	tempPath = ""
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open upload checkpoint directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return fmt.Errorf("sync upload checkpoint directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close upload checkpoint directory: %w", closeErr)
	}
	return nil
}

func (u *GenericUploader) matches(s *uploaderResumeState, req transfer.TransferRequest, info os.FileInfo, chunkSize int64) bool {
	if s == nil {
		return false
	}
	return (s.Phase == uploadCheckpointUploading || s.Phase == uploadCheckpointCompleting || s.Phase == uploadCheckpointCompleted) &&
		s.UploadID != "" &&
		s.Bucket == req.Bucket &&
		s.FileModUnixNano == info.ModTime().UnixNano() &&
		s.SourcePath == req.SourcePath &&
		s.GUID == req.GUID &&
		s.ObjectKey == effectiveObjectKey(req) &&
		s.FileSize == info.Size() &&
		s.ChunkSize == chunkSize &&
		s.RoutingFingerprint != "" &&
		s.RoutingFingerprint == uploadRoutingFingerprint(req.Metadata)
}

func completionParts(state *uploaderResumeState) ([]transfer.MultipartPart, error) {
	if state == nil || len(state.CompletionParts) == 0 {
		return nil, fmt.Errorf("%w: checkpoint has no completion manifest", errMultipartCompletionRecoveryRequired)
	}
	parts := append([]transfer.MultipartPart(nil), state.CompletionParts...)
	for i, part := range parts {
		if part.PartNumber <= 0 || (i > 0 && parts[i-1].PartNumber >= part.PartNumber) {
			return nil, fmt.Errorf("%w: checkpoint completion manifest is not sorted and unique", errMultipartCompletionRecoveryRequired)
		}
	}
	if state.CompletionFingerprint != "" && state.CompletionFingerprint != uploadPartsFingerprint(parts) {
		return nil, fmt.Errorf("%w: checkpoint completion manifest fingerprint mismatch", errMultipartCompletionRecoveryRequired)
	}
	return parts, nil
}

func uploadPartsFingerprint(parts []transfer.MultipartPart) string {
	encoded, _ := json.Marshal(parts)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

var uploadCheckpointWriter = writeUploadCheckpoint
var removeMultipartCheckpoint = os.Remove

func emitProgress(ctx context.Context, delta, total int64) {
	progress := common.GetProgress(ctx)
	if progress == nil {
		return
	}
	_ = progress(common.ProgressEvent{
		Event:          "progress",
		Oid:            common.GetOid(ctx),
		BytesSinceLast: delta,
		BytesSoFar:     total,
	})
}

type multipartProgressTracker struct {
	mu                sync.Mutex
	onProgress        common.ProgressCallback
	oid               string
	total             int64
	committed         int64
	active            map[int]int64
	lastReportedSoFar int64
}

func newMultipartProgressTracker(ctx context.Context, oid string, total int64) *multipartProgressTracker {
	return &multipartProgressTracker{
		onProgress: common.GetProgress(ctx),
		oid:        oid,
		total:      total,
		active:     make(map[int]int64),
	}
}

func (m *multipartProgressTracker) ResetPart(partNum int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[partNum] = 0
}

func (m *multipartProgressTracker) AdvancePart(partNum int, delta int64) error {
	if m == nil || m.onProgress == nil || delta <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[partNum] += delta
	return m.emitLocked(false)
}

func (m *multipartProgressTracker) CompletePart(partNum int, partSize int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.committed += partSize
	delete(m.active, partNum)
	return m.emitLocked(false)
}

func (m *multipartProgressTracker) CompleteUpload() error {
	if m == nil || m.onProgress == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.emitLocked(true)
}

func (m *multipartProgressTracker) emitLocked(final bool) error {
	if m.onProgress == nil {
		return nil
	}
	visible := m.currentVisibleLocked(final)
	if visible < m.lastReportedSoFar {
		visible = m.lastReportedSoFar
	}
	delta := visible - m.lastReportedSoFar
	if delta <= 0 {
		return nil
	}
	if err := m.onProgress(common.ProgressEvent{
		Event:          "progress",
		Oid:            m.oid,
		BytesSinceLast: delta,
		BytesSoFar:     visible,
	}); err != nil {
		return progressCallbackError{err: err}
	}
	m.lastReportedSoFar = visible
	return nil
}

func (m *multipartProgressTracker) currentVisibleLocked(final bool) int64 {
	current := m.committed
	for _, bytes := range m.active {
		current += bytes
	}
	if !final && m.total > 0 && current >= m.total {
		return m.total - 1
	}
	return current
}

type multipartPartProgressReader struct {
	reader           io.Reader
	tracker          *multipartProgressTracker
	partNum          int
	totalBytes       int64
	bytesSinceReport int64
}

func newMultipartPartProgressReader(reader io.Reader, tracker *multipartProgressTracker, partNum int, totalBytes int64) *multipartPartProgressReader {
	return &multipartPartProgressReader{reader: reader, tracker: tracker, partNum: partNum, totalBytes: totalBytes}
}

func (m *multipartPartProgressReader) Read(p []byte) (int, error) {
	n, err := m.reader.Read(p)
	if n > 0 && m.tracker != nil {
		m.bytesSinceReport += int64(n)
		if m.bytesSinceReport >= common.OnProgressThreshold {
			if progressErr := m.tracker.AdvancePart(m.partNum, m.bytesSinceReport); progressErr != nil {
				return n, progressErr
			}
			m.bytesSinceReport = 0
		}
	}
	return n, err
}

func (m *multipartPartProgressReader) FlushPendingProgress() error {
	if m.tracker == nil || m.bytesSinceReport <= 0 {
		return nil
	}
	delta := m.bytesSinceReport
	m.bytesSinceReport = 0
	return m.tracker.AdvancePart(m.partNum, delta)
}

func (m *multipartPartProgressReader) Size() int64 {
	if m == nil {
		return 0
	}
	return m.totalBytes
}

func completedMultipartBytes(state *uploaderResumeState, fileSize, chunkSize int64) int64 {
	if state == nil || len(state.Completed) == 0 {
		return 0
	}
	var total int64
	for partNum := range state.Completed {
		offset := int64(partNum-1) * chunkSize
		partSize := chunkSize
		if offset+partSize > fileSize {
			partSize = fileSize - offset
		}
		if partSize > 0 {
			total += partSize
		}
	}
	return total
}

func isProgressCallbackError(err error) bool {
	var target progressCallbackError
	return errors.As(err, &target)
}
