package records

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalBulkMissingSHA256Fiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkMissingSHA256Request
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Sha256) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: organization, project, and sha256 values are required")
		}

		normalized, err := normalizeMissingSHA256(req.Sha256)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if len(normalized) > maxInternalBulkMissingSHA256 {
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString(fmt.Sprintf("too many sha256 values: maximum is %d", maxInternalBulkMissingSHA256))
		}

		missing, err := objectService.ListMissingScopedSHA256(c.Context(), req.Organization, req.Project, normalized)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.BulkMissingSHA256Response{Checked: int32(len(normalized)), MissingSha256: missing})
	}
}

func normalizeMissingSHA256(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		value = strings.TrimPrefix(strings.ToLower(value), "sha256:")
		if value == "" {
			continue
		}
		if len(value) != 64 {
			return nil, fmt.Errorf("invalid sha256 checksum %q", raw)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return nil, fmt.Errorf("invalid sha256 checksum %q", raw)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Invalid request body: sha256 values are required")
	}
	return out, nil
}

func handleInternalBulkHashesFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkHashesRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}

		normalized := normalizeBulkHashes(req.Hashes)
		res, err := objectService.GetObjectsByChecksums(c.Context(), normalized, "read")
		if err != nil {
			return response.HandleError(c, err)
		}

		finalRes := make(map[string][]internalapi.InternalRecord, len(req.Hashes))
		for i, h := range req.Hashes {
			typ, val := objects.ParseHashQuery(h, "")
			matches := []objects.Record{}
			if i < len(normalized) {
				matches = res[normalized[i]]
			}
			if typ != "" {
				filtered := make([]objects.Record, 0, len(matches))
				for _, m := range matches {
					if objects.RecordHasChecksumTypeAndValue(m, typ, val) {
						filtered = append(filtered, m)
					}
				}
				matches = filtered
			}
			compatibilityMatches := make([]internalapi.InternalRecord, 0, len(matches))
			for _, match := range matches {
				compatibilityMatches = append(compatibilityMatches, ToInternalRecord(match))
			}
			finalRes[h] = compatibilityMatches
		}

		return c.JSON(struct {
			Results map[string][]internalapi.InternalRecord
		}{Results: finalRes})
	}
}

func handleInternalBulkSHA256ValidityFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkSHA256ValidityRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if req.Sha256 == nil || len(*req.Sha256) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: sha256 values are required")
		}

		hashes := make([]string, 0, len(*req.Sha256))
		out := make(map[string]bool, len(*req.Sha256))
		for _, raw := range *req.Sha256 {
			hash := strings.TrimSpace(raw)
			if hash == "" {
				continue
			}
			hashes = append(hashes, hash)
			out[hash] = false
		}
		if len(hashes) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: sha256 values are required")
		}

		records, err := objectService.GetObjectsByChecksums(c.Context(), hashes, "read")
		if err != nil {
			return response.HandleError(c, err)
		}
		for _, hash := range hashes {
			for _, obj := range records[hash] {
				if objects.RecordHasChecksumTypeAndValue(obj, "sha256", hash) {
					out[hash] = true
					break
				}
			}
		}
		return c.JSON(out)
	}
}

func normalizeBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := objects.ParseHashQuery(h, "")
		normalized = append(normalized, val)
	}
	return normalized
}

func normalizeNonEmptyBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := objects.ParseHashQuery(h, "")
		if strings.TrimSpace(val) == "" {
			continue
		}
		normalized = append(normalized, val)
	}
	return normalized
}
