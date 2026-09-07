package services

import (
	"encoding/json"
	"testing"
)

func TestTransferMetricsDTOJSONContract(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(TransferBreakdownResponse{
		GroupBy: "provider",
		Data: []TransferAttributionBreakdown{{
			Key:             "provider-a",
			Organization:    "org",
			Project:         "project",
			Provider:        "s3",
			Bucket:          "bucket-a",
			SHA256:          "sha256",
			ActorEmail:      "user@example.com",
			ActorSubject:    "subject",
			EventCount:      2,
			BytesRequested:  20,
			BytesDownloaded: 18,
			BytesUploaded:   2,
		}},
	})
	if err != nil {
		t.Fatalf("marshal transfer DTO: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal transfer DTO: %v", err)
	}
	if got["group_by"] != "provider" {
		t.Fatalf("unexpected group_by JSON: %v", got["group_by"])
	}
	rows, ok := got["data"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("unexpected data JSON: %v", got["data"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok || row["sha256"] != "sha256" || row["bytes_downloaded"] != float64(18) {
		t.Fatalf("unexpected breakdown JSON: %v", rows[0])
	}
}
