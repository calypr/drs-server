package drsapi

import (
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/api/apiutil"
	"github.com/calypr/syfon/internal/api/attribution"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/urlmanager"
	"github.com/gofiber/fiber/v3"
)

func handleGetAccessURLFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("object_id")
		accessID := c.Params("access_id")

		obj, err := om.GetObject(c.Context(), id, "read")
		if err != nil {
			return apiutil.HandleError(c, err)
		}

		targetURL := accessURLForID(obj, accessID)
		if targetURL == "" {
			return c.Status(fiber.StatusNotFound).JSON(drs.Error{Msg: common.Ptr("Access ID not found or has no URL")})
		}

		opts := urlmanager.SignOptions{Method: http.MethodGet}
		if obj.Name != nil {
			opts.DownloadFilename = common.DownloadFilename(*obj.Name)
		}
		signed, err := om.SignObjectURL(c.Context(), obj, targetURL, opts)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		if err := attribution.RecordAccessIssued(c.Context(), om, obj, attribution.AccessDetails{
			Direction:  models.ProviderTransferDirectionDownload,
			AccessID:   accessID,
			StorageURL: targetURL,
		}); err != nil {
			return apiutil.HandleError(c, err)
		}

		return c.JSON(drs.AccessURL{Url: signed})
	}
}

func accessURLForID(obj *models.InternalObject, accessID string) string {
	if obj == nil || obj.AccessMethods == nil {
		return ""
	}
	accessID = strings.TrimSpace(accessID)
	if accessID == "" {
		return ""
	}
	legacyMatches := make([]string, 0, 1)
	for _, am := range *obj.AccessMethods {
		if am.AccessUrl == nil || strings.TrimSpace(am.AccessUrl.Url) == "" {
			continue
		}
		if strings.EqualFold(common.StringVal(am.AccessId), accessID) {
			return am.AccessUrl.Url
		}
		if strings.EqualFold(strings.TrimSpace(string(am.Type)), accessID) {
			legacyMatches = append(legacyMatches, am.AccessUrl.Url)
		}
	}
	if len(legacyMatches) == 1 {
		return legacyMatches[0]
	}
	return ""
}

func handleGetBulkAccessURLFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		var body drs.BulkObjectAccessId
		if err := c.Bind().JSON(&body); err != nil || body.BulkObjectAccessIds == nil {
			return c.Status(fiber.StatusBadRequest).JSON(drs.Error{Msg: common.Ptr("Invalid request body")})
		}

		requested := 0
		resolved := make([]drs.BulkAccessURL, 0)
		unresolvedIDs := make([]string, 0)

		for _, item := range *body.BulkObjectAccessIds {
			objectID := strings.TrimSpace(common.StringVal(item.BulkObjectId))
			if objectID == "" || item.BulkAccessIds == nil || len(*item.BulkAccessIds) == 0 {
				requested++
				if objectID != "" {
					unresolvedIDs = append(unresolvedIDs, objectID)
				}
				continue
			}

			obj, err := om.GetObject(c.Context(), objectID, "read")
			if err != nil {
				requested += len(*item.BulkAccessIds)
				unresolvedIDs = append(unresolvedIDs, objectID)
				continue
			}

			for _, rawAccessID := range *item.BulkAccessIds {
				requested++
				accessID := strings.TrimSpace(rawAccessID)
				targetURL := accessURLForID(obj, accessID)
				if accessID == "" || targetURL == "" {
					unresolvedIDs = append(unresolvedIDs, objectID)
					continue
				}

				opts := urlmanager.SignOptions{Method: http.MethodGet}
				if obj.Name != nil {
					opts.DownloadFilename = common.DownloadFilename(*obj.Name)
				}
				signed, err := om.SignObjectURL(c.Context(), obj, targetURL, opts)
				if err != nil {
					unresolvedIDs = append(unresolvedIDs, objectID)
					continue
				}
				if err := attribution.RecordAccessIssued(c.Context(), om, obj, attribution.AccessDetails{
					Direction:  models.ProviderTransferDirectionDownload,
					AccessID:   accessID,
					StorageURL: targetURL,
				}); err != nil {
					unresolvedIDs = append(unresolvedIDs, objectID)
					continue
				}
				resolved = append(resolved, drs.BulkAccessURL{
					DrsObjectId: common.Ptr(objectID),
					DrsAccessId: common.Ptr(accessID),
					Url:         signed,
				})
			}
		}

		resp := fiber.Map{
			"resolved_drs_object_access_urls": resolved,
			"summary": drs.Summary{
				Requested:  common.Ptr(requested),
				Resolved:   common.Ptr(len(resolved)),
				Unresolved: common.Ptr(requested - len(resolved)),
			},
		}
		if len(unresolvedIDs) > 0 {
			resp["unresolved_drs_objects"] = []fiber.Map{{
				"error_code": fiber.StatusNotFound,
				"object_ids": unresolvedIDs,
			}}
		}
		return c.JSON(resp)
	}
}
