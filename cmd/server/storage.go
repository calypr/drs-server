package server

import (
	"log/slog"

	"github.com/calypr/syfon/internal/storage"
	storageazure "github.com/calypr/syfon/internal/storage/azure"
	storagefile "github.com/calypr/syfon/internal/storage/file"
	storagegcs "github.com/calypr/syfon/internal/storage/gcs"
	storages3 "github.com/calypr/syfon/internal/storage/s3"
)

type storageInvalidator struct {
	manager *storage.Manager
}

func (i *storageInvalidator) InvalidateBucket(bucket string) {
	if i != nil && i.manager != nil {
		i.manager.InvalidateBucket(bucket)
	}
}

func newStorageManager(credentials storage.CredentialLookup, fileRoot string, logger *slog.Logger) (*storage.Manager, error) {
	registrations := []storage.Registration{
		storages3.New(credentials),
		storagegcs.New(credentials),
		storageazure.New(credentials),
	}
	fileRegistration, err := storagefile.New(fileRoot)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to initialize file storage", "err", err)
		}
	} else {
		registrations = append(registrations, fileRegistration)
	}
	return storage.NewManager(credentials, registrations...)
}
