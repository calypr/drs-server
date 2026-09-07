package drsapi

import (
	"encoding/json"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/api/apiutil"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/gofiber/fiber/v3"
)

func handleGetObjectFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("object_id")
		obj, err := om.GetObject(c.Context(), id, "")
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(drsObjectPayload(*obj))
	}
}

func handleGetBulkObjectsFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		var body struct {
			BulkObjectIds []string `json:"bulk_object_ids"`
		}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(drs.Error{Msg: common.Ptr("Invalid request body")})
		}

		objects, err := om.GetBulkObjects(c.Context(), body.BulkObjectIds, "")
		if err != nil {
			return apiutil.HandleError(c, err)
		}

		resolved := make([]any, 0, len(objects))
		for _, obj := range objects {
			resolved = append(resolved, drsObjectPayload(obj))
		}

		return c.JSON(fiber.Map{
			"resolved_drs_object": resolved,
			"summary": drs.Summary{
				Requested: common.Ptr(len(body.BulkObjectIds)),
				Resolved:  common.Ptr(len(resolved)),
			},
		})
	}
}

func handleGetObjectsByChecksumFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		checksum := c.Params("checksum")
		fetched, err := om.GetObjectsByChecksum(c.Context(), checksum, "")
		if err != nil {
			return apiutil.HandleError(c, err)
		}

		resolved := make([]any, 0)
		for _, obj := range fetched {
			resolved = append(resolved, drsObjectPayload(obj))
		}

		return c.JSON(fiber.Map{
			"resolved_drs_object": resolved,
			"summary": drs.Summary{
				Requested: common.Ptr(1),
				Resolved:  common.Ptr(len(resolved)),
			},
		})
	}
}

func drsObjectPayload(obj models.InternalObject) map[string]any {
	var payload map[string]any
	data, err := json.Marshal(obj.DrsObject)
	if err == nil {
		if err := json.Unmarshal(data, &payload); err == nil {
			return payload
		}
	}
	payload = map[string]any{}
	if obj.Id != "" {
		payload["id"] = obj.Id
	}
	payload["self_uri"] = obj.SelfUri
	return payload
}
