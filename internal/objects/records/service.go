package records

import "time"

const (
	objectMethodRead   = "read"
	objectMethodCreate = "create"
	objectMethodUpdate = "update"
	objectMethodDelete = "delete"
)

// Service owns stateful record lookup and mutation operations. The complete
// persistence capability is explicit, so every operation observes the same
// store and no optional fallback path can silently change behavior.
type Service struct {
	store ObjectStore
	now   func() time.Time
}

func NewService(store ObjectStore) *Service {
	return &Service{store: store, now: time.Now}
}
