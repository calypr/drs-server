package postgres

import (
	"github.com/calypr/syfon/internal/models"
	"strings"
	"time"
)

func defaultProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "s3"
	}
	return provider
}

func uniqueObjectsByID(objs []models.InternalObject) []models.InternalObject {
	seen := make(map[string]struct{}, len(objs))
	out := make([]models.InternalObject, 0, len(objs))
	for _, o := range objs {
		if _, ok := seen[o.Id]; ok {
			continue
		}
		seen[o.Id] = struct{}{}
		out = append(out, o)
	}
	return out
}

func latestUsageTime(ts ...*time.Time) *time.Time {
	var latest *time.Time
	for _, t := range ts {
		if t == nil {
			continue
		}
		if latest == nil || t.After(*latest) {
			copyT := *t
			latest = &copyT
		}
	}
	return latest
}
