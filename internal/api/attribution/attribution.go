package attribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/requestmeta"
)

type AccessDetails struct {
	AccessID       string
	Direction      string
	StorageURL     string
	RangeStart     *int64
	RangeEnd       *int64
	BytesRequested int64
	ClientName     string
	ClientVersion  string
}

func RecordAccessIssued(ctx context.Context, om *core.ObjectManager, obj *models.InternalObject, details AccessDetails) error {
	if om == nil || obj == nil {
		return nil
	}
	ev := EventFromObject(ctx, obj, models.TransferEventAccessIssued, details)
	if ev.EventID == "" {
		return nil
	}
	return om.RecordTransferAttributionEvents(ctx, []models.TransferAttributionEvent{ev})
}

func EventFromObject(ctx context.Context, obj *models.InternalObject, eventType string, details AccessDetails) models.TransferAttributionEvent {
	if obj == nil {
		return models.TransferAttributionEvent{}
	}
	storageURL := strings.TrimSpace(details.StorageURL)
	accessID := strings.TrimSpace(details.AccessID)
	if storageURL == "" || accessID == "" {
		for _, am := range accessMethods(obj) {
			if accessID != "" && !strings.EqualFold(accessMethodID(am), accessID) {
				continue
			}
			if accessID == "" {
				accessID = accessMethodID(am)
			}
			if am.AccessUrl != nil {
				storageURL = strings.TrimSpace(am.AccessUrl.Url)
			}
			if storageURL != "" {
				break
			}
		}
	}
	org, project := scopeForAccess(obj, accessID)
	provider, bucket := providerBucket(storageURL)
	direction := strings.ToLower(strings.TrimSpace(details.Direction))
	switch direction {
	case models.ProviderTransferDirectionUpload:
	default:
		direction = models.ProviderTransferDirectionDownload
	}
	sha := sha256ForObject(obj)
	bytesRequested := details.BytesRequested
	if bytesRequested <= 0 && details.RangeStart != nil && details.RangeEnd != nil && *details.RangeEnd >= *details.RangeStart {
		bytesRequested = *details.RangeEnd - *details.RangeStart + 1
	}
	if bytesRequested <= 0 {
		bytesRequested = obj.Size
	}
	when := time.Now().UTC()
	ev := models.TransferAttributionEvent{
		EventType:      eventType,
		Direction:      direction,
		EventTime:      when,
		RequestID:      requestmeta.GetRequestID(ctx),
		ObjectID:       obj.Id,
		SHA256:         sha,
		ObjectSize:     obj.Size,
		Organization:   org,
		Project:        project,
		AccessID:       accessID,
		Provider:       provider,
		Bucket:         bucket,
		StorageURL:     storageURL,
		RangeStart:     details.RangeStart,
		RangeEnd:       details.RangeEnd,
		BytesRequested: bytesRequested,
		ActorEmail:     ActorEmail(ctx),
		ActorSubject:   ActorSubject(ctx),
		AuthMode:       authMode(ctx),
		ClientName:     details.ClientName,
		ClientVersion:  details.ClientVersion,
	}
	ev.EventID = EventID(ev)
	ev.AccessGrantID = AccessGrantID(ev)
	return ev
}

func AccessGrantID(ev models.TransferAttributionEvent) string {
	parts := []string{
		ev.ObjectID,
		ev.SHA256,
		ev.Organization,
		ev.Project,
		ev.AccessID,
		ev.Provider,
		ev.Bucket,
		ev.StorageURL,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func EventID(ev models.TransferAttributionEvent) string {
	if strings.TrimSpace(ev.EventID) != "" {
		return ev.EventID
	}
	parts := []string{
		ev.EventType,
		ev.Direction,
		ev.RequestID,
		ev.ObjectID,
		ev.SHA256,
		ev.Organization,
		ev.Project,
		ev.AccessID,
		ev.Provider,
		ev.Bucket,
		ev.StorageURL,
		fmt.Sprint(int64Value(ev.RangeStart)),
		fmt.Sprint(int64Value(ev.RangeEnd)),
		fmt.Sprint(ev.BytesRequested),
		fmt.Sprint(ev.BytesCompleted),
		ev.ActorEmail,
		ev.ActorSubject,
		ev.TransferSessionID,
	}
	if ev.RequestID == "" && ev.TransferSessionID == "" {
		parts = append(parts, ev.EventTime.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func ActorSubject(ctx context.Context) string {
	return strings.TrimSpace(access.FromContext(ctx).Subject)
}

func ActorEmail(ctx context.Context) string {
	claims := access.FromContext(ctx).Claims
	for _, key := range []string{"email", "preferred_username", "username"} {
		if v, ok := claims[key].(string); ok && strings.Contains(v, "@") {
			return strings.TrimSpace(v)
		}
	}
	sub := ActorSubject(ctx)
	if strings.Contains(sub, "@") {
		return sub
	}
	return ""
}

func authMode(ctx context.Context) string {
	return strings.TrimSpace(access.FromContext(ctx).Mode)
}

func accessMethods(obj *models.InternalObject) []drs.AccessMethod {
	if obj == nil || obj.AccessMethods == nil {
		return nil
	}
	return *obj.AccessMethods
}

func accessMethodID(am drs.AccessMethod) string {
	if am.AccessId != nil && strings.TrimSpace(*am.AccessId) != "" {
		return strings.TrimSpace(*am.AccessId)
	}
	return strings.TrimSpace(string(am.Type))
}

func scopeForAccess(obj *models.InternalObject, accessID string) (string, string) {
	for _, am := range accessMethods(obj) {
		if accessID != "" && !strings.EqualFold(accessMethodID(am), accessID) {
			continue
		}
	}
	if len(obj.Authorizations) > 0 {
		for org, projects := range obj.Authorizations {
			if len(projects) == 0 {
				return org, ""
			}
			return org, projects[0]
		}
	}
	return "", ""
}

func providerBucket(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ""
	}
	return common.ProviderFromScheme(u.Scheme), strings.TrimSpace(u.Host)
}

func sha256ForObject(obj *models.InternalObject) string {
	for _, c := range obj.Checksums {
		if strings.EqualFold(c.Type, "sha256") {
			return strings.TrimSpace(c.Checksum)
		}
	}
	return ""
}

func int64Value(v *int64) int64 {
	if v == nil {
		return -1
	}
	return *v
}
