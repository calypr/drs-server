package core

import (
	"context"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/urlmanager"
)

func (m *ObjectManager) SavePendingLFSMeta(ctx context.Context, entries []models.PendingLFSMeta) error {
	return m.db.SavePendingLFSMeta(ctx, entries)
}

func (m *ObjectManager) GetPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error) {
	return m.db.GetPendingLFSMeta(ctx, oid)
}

func (m *ObjectManager) PopPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error) {
	return m.db.PopPendingLFSMeta(ctx, oid)
}

func (m *ObjectManager) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	return m.uM.InitMultipartUpload(ctx, bucket, key)
}

func (m *ObjectManager) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNum int32) (string, error) {
	return m.uM.SignMultipartPart(ctx, bucket, key, uploadID, partNum)
}

func (m *ObjectManager) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []urlmanager.MultipartPart) error {
	return m.uM.CompleteMultipartUpload(ctx, bucket, key, uploadID, parts)
}

func (m *ObjectManager) GetServiceInfo(ctx context.Context) (*drs.Service, error) {
	return m.db.GetServiceInfo(ctx)
}
