package core

import (
	"context"

	"github.com/calypr/syfon/internal/models"
)

func (m *ObjectManager) RecordDownload(ctx context.Context, id string) error {
	return m.db.RecordFileDownload(ctx, id)
}

func (m *ObjectManager) RecordUpload(ctx context.Context, id string) error {
	return m.db.RecordFileUpload(ctx, id)
}

func (m *ObjectManager) RecordTransferAttributionEvents(ctx context.Context, events []models.TransferAttributionEvent) error {
	return m.db.RecordTransferAttributionEvents(ctx, events)
}

func (m *ObjectManager) RecordProviderTransferEvents(ctx context.Context, events []models.ProviderTransferEvent) error {
	return m.db.RecordProviderTransferEvents(ctx, events)
}
