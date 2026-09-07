package lfs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type lfsTestServicePorts struct {
	objectReader  *lfsObjectReaderFake
	objectWriter  *lfsObjectWriterFake
	contentReader *lfsContentReaderFake
	aliases       *lfsAliasStoreFake
	credentials   *lfsCredentialReaderFake
	pending       *lfsPendingStoreFake
	events        *lfsEventRecorderFake
	fileCounters  *lfsFileCounterFake
}

func newLFSTestPorts(records map[string]*objects.Record, credentials map[string]buckets.Credential) *lfsTestServicePorts {
	return &lfsTestServicePorts{
		objectReader:  &lfsObjectReaderFake{records: records},
		objectWriter:  &lfsObjectWriterFake{records: records},
		contentReader: &lfsContentReaderFake{records: records},
		aliases:       &lfsAliasStoreFake{aliases: map[string]string{}},
		credentials:   &lfsCredentialReaderFake{credentials: credentials},
		pending:       &lfsPendingStoreFake{entries: map[string]transfers.PendingMetadata{}},
		events:        &lfsEventRecorderFake{},
		fileCounters:  &lfsFileCounterFake{},
	}
}

type lfsObjectReaderFake struct {
	records map[string]*objects.Record
}

var _ objects.RecordReader = (*lfsObjectReaderFake)(nil)

func (f *lfsObjectReaderFake) GetObject(_ context.Context, id string) (*objects.Record, error) {
	record, ok := f.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	copyRecord := *record
	return &copyRecord, nil
}

func (f *lfsObjectReaderFake) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		record, ok := f.records[id]
		if !ok {
			continue
		}
		result = append(result, *record)
	}
	return result, nil
}

type lfsObjectWriterFake struct {
	records map[string]*objects.Record
}

var _ objects.RecordWriter = (*lfsObjectWriterFake)(nil)

func (f *lfsObjectWriterFake) DeleteObject(_ context.Context, id string) error {
	delete(f.records, id)
	return nil
}

func (f *lfsObjectWriterFake) CreateObject(_ context.Context, record *objects.Record) error {
	copyRecord := *record
	f.records[string(record.Id)] = &copyRecord
	return nil
}

func (f *lfsObjectWriterFake) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(f.records, id)
	}
	return nil
}

func (f *lfsObjectWriterFake) RegisterObjects(ctx context.Context, records []objects.Record) error {
	for i := range records {
		if err := f.CreateObject(ctx, &records[i]); err != nil {
			return err
		}
	}
	return nil
}

func (f *lfsObjectWriterFake) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	return f.RegisterObjects(ctx, records)
}

type lfsContentReaderFake struct {
	records map[string]*objects.Record
}

var _ objects.ContentReader = (*lfsContentReaderFake)(nil)

func (f *lfsContentReaderFake) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	result := make([]objects.Record, 0)
	for _, record := range f.records {
		if recordMatchesChecksum(record, checksum) {
			result = append(result, *record)
		}
	}
	return result, nil
}

func (f *lfsContentReaderFake) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := f.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func recordMatchesChecksum(record *objects.Record, checksum string) bool {
	if record == nil {
		return false
	}
	if string(record.Id) == checksum {
		return true
	}
	for _, candidate := range record.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
}

type lfsAliasStoreFake struct {
	aliases map[string]string
}

var _ objects.AliasStore = (*lfsAliasStoreFake)(nil)

func (f *lfsAliasStoreFake) DeleteObjectAlias(_ context.Context, aliasID string) error {
	delete(f.aliases, aliasID)
	return nil
}

func (f *lfsAliasStoreFake) CreateObjectAlias(_ context.Context, aliasID, canonicalObjectID string) error {
	f.aliases[aliasID] = canonicalObjectID
	return nil
}

func (f *lfsAliasStoreFake) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := f.aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: object alias not found", faults.ErrNotFound)
	}
	return canonicalID, nil
}

type lfsCredentialReaderFake struct {
	credentials map[string]buckets.Credential
}

var _ buckets.CredentialReader = (*lfsCredentialReaderFake)(nil)

func (f *lfsCredentialReaderFake) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	credential, ok := f.credentials[bucket]
	if !ok {
		return nil, fmt.Errorf("%w: credential not found", faults.ErrNotFound)
	}
	copyCredential := credential
	return &copyCredential, nil
}

func (f *lfsCredentialReaderFake) ListS3Credentials(_ context.Context) ([]buckets.Credential, error) {
	keys := make([]string, 0, len(f.credentials))
	for key := range f.credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]buckets.Credential, 0, len(keys))
	for _, key := range keys {
		result = append(result, f.credentials[key])
	}
	return result, nil
}

type lfsPendingStoreFake struct {
	entries map[string]transfers.PendingMetadata
}

var _ transfers.PendingStore = (*lfsPendingStoreFake)(nil)

func (f *lfsPendingStoreFake) SavePendingLFSMeta(_ context.Context, entries []transfers.PendingMetadata) error {
	for _, entry := range entries {
		f.entries[entry.OID] = entry
	}
	return nil
}

func (f *lfsPendingStoreFake) GetPendingLFSMeta(_ context.Context, oid string) (*transfers.PendingMetadata, error) {
	entry, ok := f.entries[oid]
	if !ok {
		return nil, fmt.Errorf("%w: pending metadata not found", faults.ErrNotFound)
	}
	return &entry, nil
}

func (f *lfsPendingStoreFake) PopPendingLFSMeta(_ context.Context, oid string) (*transfers.PendingMetadata, error) {
	entry, ok := f.entries[oid]
	if !ok {
		return nil, fmt.Errorf("%w: pending metadata not found", faults.ErrNotFound)
	}
	delete(f.entries, oid)
	return &entry, nil
}

type lfsEventRecorderFake struct {
	events []usage.Event
}

var _ transfers.EventRecorder = (*lfsEventRecorderFake)(nil)

func (f *lfsEventRecorderFake) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	f.events = append(f.events, events...)
	return nil
}

type lfsFileCounterFake struct {
	uploads   []string
	downloads []string
}

var _ usage.FileCounterRecorder = (*lfsFileCounterFake)(nil)

func (f *lfsFileCounterFake) RecordFileUpload(_ context.Context, objectID string) error {
	f.uploads = append(f.uploads, objectID)
	return nil
}

func (f *lfsFileCounterFake) RecordFileDownload(_ context.Context, objectID string) error {
	f.downloads = append(f.downloads, objectID)
	return nil
}

func newLFSTransferService(storageFake *lfsTestStorage, ports *lfsTestServicePorts) *transfers.Service {
	return transfers.NewService(transfers.Dependencies{
		Access:      storageFake,
		Multipart:   storageFake,
		Credentials: ports.credentials,
		Pending:     ports.pending,
		Events:      ports.events,
	})
}

var _ transfers.AccessPort = (*lfsTestStorage)(nil)
var _ transfers.MultipartPort = (*lfsTestStorage)(nil)
