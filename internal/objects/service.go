package objects

import (
	"context"
	"time"
)

const (
	objectMethodRead   = "read"
	objectMethodCreate = "create"
	objectMethodUpdate = "update"
	objectMethodDelete = "delete"
)

// Service owns stateful object lookup and mutation operations.
type Service struct {
	store    ObjectStore
	resolver PathPrefixResolver
	now      func() time.Time
}

func NewService(store ObjectStore, resolver PathPrefixResolver) *Service {
	return &Service{store: store, resolver: resolver, now: time.Now}
}

// ObjectStore is the persistence capability required by Service.
type ObjectStore interface {
	GetObject(context.Context, string) (*Record, error)
	GetBulkObjects(context.Context, []string) ([]Record, error)
	DeleteObject(context.Context, string) error
	BulkDeleteObjects(context.Context, []string) error
	RegisterObjects(context.Context, []Record) error
	ReplaceObjects(context.Context, []Record) error
	UpdateObjectAccessMethods(context.Context, string, []AccessMethod) error
	BulkUpdateAccessMethods(context.Context, map[string][]AccessMethod) error
	RemoveObjectControlledAccess(context.Context, string, string) error
	RemoveObjectControlledAccessBulk(context.Context, []string, string) (int, error)
	CreateObjectAlias(context.Context, string, string) error
	ResolveObjectAlias(context.Context, string) (string, error)
	GetObjectsByChecksum(context.Context, string) ([]Record, error)
	GetObjectsByChecksums(context.Context, []string) (map[string][]Record, error)
	ListScopedObjectIDsByChecksums(context.Context, string, string, []string) (map[string][]string, error)
	ListObjectIDsByScope(context.Context, string, string) ([]string, error)
	ListObjectIDsByResources(context.Context, []string, bool) ([]string, error)
	ListObjectIDsPageByScope(context.Context, string, string, string, int, int) ([]string, error)
	ListObjectIDsPageByURL(context.Context, string, string, string, string, int, int, []string, bool, bool) ([]string, error)
	ListObjectIDsByScopeAndResources(context.Context, string, string, []string, bool) ([]string, error)
	ListObjectIDsByChecksumsAndResources(context.Context, []string, []string, bool, bool) (map[string][]string, error)
}
