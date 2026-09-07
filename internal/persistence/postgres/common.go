package postgres

import (
	"strings"
	"time"

	"github.com/calypr/syfon/internal/objects"
)

func defaultProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "s3"
	}
	return provider
}

func postgresPtr[T any](value T) *T {
	return &value
}

func postgresStringVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func postgresTimeVal(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func uniqueObjectsByID(objs []objects.Record) []objects.Record {
	seen := make(map[string]struct{}, len(objs))
	out := make([]objects.Record, 0, len(objs))
	for _, o := range objs {
		if _, ok := seen[string(o.Id)]; ok {
			continue
		}
		seen[string(o.Id)] = struct{}{}
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
