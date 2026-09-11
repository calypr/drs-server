package objects

import (
	"context"

	"github.com/calypr/syfon/apigen/drs"
)

const (
	objectMethodRead   = "read"
	objectMethodCreate = "create"
	objectMethodUpdate = "update"
	objectMethodDelete = "delete"
)

// Service owns stateful object lookup and mutation operations.
type Service struct {
	store ObjectStore
}

func NewService(store ObjectStore) *Service {
	return &Service{store: store}
}

// ObjectStore is the persistence capability required by Service.
type ObjectStore interface {
	// GetObject resolves a physical ID or alias to its stored row.
	GetObject(context.Context, string) (*drs.DrsObject, error)
	GetBulkObjects(context.Context, []string) ([]drs.DrsObject, error)
	DeleteObject(context.Context, string) error
	BulkDeleteObjects(context.Context, []string) error
	RegisterObjects(context.Context, []drs.DrsObject) error
	RepairCanonicalDuplicates(context.Context, []CanonicalRepair) error
	ReplaceObjects(context.Context, []drs.DrsObject) error
	UpdateObjectAccessMethods(context.Context, string, []drs.AccessMethod) error
	BulkUpdateAccessMethods(context.Context, map[string][]drs.AccessMethod) error
	RemoveObjectControlledAccess(context.Context, string, string) error
	RemoveObjectControlledAccessBulk(context.Context, []string, string) (int, error)
	CreateObjectAlias(context.Context, string, string) error
	ResolveObjectAlias(context.Context, string) (string, error)
	GetObjectsByChecksums(context.Context, []string) (map[string][]drs.DrsObject, error)
	GetPublicReadByIDs(context.Context, []string) (map[string]bool, error)
	ListScopedObjectIDsByChecksums(context.Context, string, string, []string) (map[string][]string, error)
	ListObjectIDsByScope(context.Context, string, string) ([]string, error)
	ListObjectIDsByResources(context.Context, []string, bool) ([]string, error)
	ListObjectIDsPageByScope(context.Context, string, string, string, int, int) ([]string, error)
	ListObjectIDsPageByURL(context.Context, string, string, string, string, int, int, []string, bool, bool) ([]string, error)
}
