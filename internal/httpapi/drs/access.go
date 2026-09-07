package drs

import (
	"strings"

	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/httpapi/response"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func handleGetAccessURLFiber(objectService *objectrecords.Service, transferService *transfers.Service) fiber.Handler {
	workflow := transfers.NewAccessWorkflow(objectService, transferService)
	return func(c fiber.Ctx) error {
		id := c.Params("object_id")
		accessID := c.Params("access_id")

		result, err := workflow.Issue(c.Context(), id, accessID)
		if err != nil {
			return response.HandleError(c, err)
		}
		if !result.Found {
			return c.Status(fiber.StatusNotFound).JSON(generated.Error{Msg: drsPtr("Access ID not found or has no URL")})
		}
		return c.JSON(generated.AccessURL{Url: result.URL})
	}
}

func handleGetBulkAccessURLFiber(objectService *objectrecords.Service, transferService *transfers.Service) fiber.Handler {
	workflow := transfers.NewAccessWorkflow(objectService, transferService)
	return func(c fiber.Ctx) error {
		var body generated.BulkObjectAccessId
		if err := c.Bind().JSON(&body); err != nil || body.BulkObjectAccessIds == nil {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}

		requests := make([]transfers.BulkAccessLookupRequest, 0, len(*body.BulkObjectAccessIds))
		for _, item := range *body.BulkObjectAccessIds {
			accessIDs := []string(nil)
			if item.BulkAccessIds != nil {
				accessIDs = append(accessIDs, (*item.BulkAccessIds)...)
			}
			requests = append(requests, transfers.BulkAccessLookupRequest{
				ObjectID:  strings.TrimSpace(drsStringValue(item.BulkObjectId)),
				AccessIDs: accessIDs,
			})
		}
		result := workflow.IssueBulk(c.Context(), requests)
		resolved := make([]generated.BulkAccessURL, 0, len(result.Resolved))
		for _, item := range result.Resolved {
			resolved = append(resolved, generated.BulkAccessURL{
				DrsObjectId: drsPtr(item.ObjectID),
				DrsAccessId: drsPtr(item.AccessID),
				Url:         item.URL,
			})
		}

		resp := fiber.Map{
			"resolved_drs_object_access_urls": resolved,
			"summary": generated.Summary{
				Requested:  drsPtr(result.Requested),
				Resolved:   drsPtr(len(resolved)),
				Unresolved: drsPtr(result.Requested - len(resolved)),
			},
		}
		if len(result.UnresolvedObjectIDs) > 0 {
			resp["unresolved_drs_objects"] = []fiber.Map{{
				"error_code": fiber.StatusNotFound,
				"object_ids": result.UnresolvedObjectIDs,
			}}
		}
		return c.JSON(resp)
	}
}
