package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func GrantID(event Event) string {
	parts := []string{
		event.ObjectID,
		event.SHA256,
		event.Organization,
		event.Project,
		event.AccessID,
		event.Provider,
		event.Bucket,
		event.StorageURL,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func EventID(event Event) string {
	if strings.TrimSpace(event.EventID) != "" {
		return event.EventID
	}
	parts := []string{
		event.EventType,
		event.Direction,
		event.RequestID,
		event.ObjectID,
		event.SHA256,
		event.Organization,
		event.Project,
		event.AccessID,
		event.Provider,
		event.Bucket,
		event.StorageURL,
		fmt.Sprint(rangeValue(event.RangeStart)),
		fmt.Sprint(rangeValue(event.RangeEnd)),
		fmt.Sprint(event.BytesRequested),
		fmt.Sprint(event.BytesCompleted),
		event.ActorEmail,
		event.ActorSubject,
		event.TransferSessionID,
	}
	if event.RequestID == "" && event.TransferSessionID == "" {
		parts = append(parts, event.EventTime.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func rangeValue(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}
