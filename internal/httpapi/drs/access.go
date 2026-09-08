package drs

import (
	"strings"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func (s *server) GetAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	workflow := transfers.NewAccessWorkflow(s.objectService, s.accessService)
	result, err := workflow.Issue(c.Context(), string(objectID), string(accessID))
	if err != nil {
		return middleware.HandleError(c, err)
	}
	if !result.Found {
		return middleware.Reject(c, fiber.StatusNotFound, "Access ID not found or has no URL")
	}
	return c.JSON(generated.AccessURL{Url: result.URL})
}

func (s *server) PostAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	return s.GetAccessURL(c, objectID, accessID)
}

func (s *server) GetBulkAccessURL(c fiber.Ctx) error {
	workflow := transfers.NewAccessWorkflow(s.objectService, s.accessService)
	var body generated.BulkObjectAccessId
	if err := c.Bind().JSON(&body); err != nil || body.BulkObjectAccessIds == nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
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
