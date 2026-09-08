package drs

import (
	"encoding/json"
	"time"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func (s *server) RegisterObjects(c fiber.Ctx) error {
	var body registerObjectsRequest
	if err := json.Unmarshal(c.Body(), &body); err != nil || len(body.Candidates) == 0 {
		var single registerObjectCandidate
		if err2 := json.Unmarshal(c.Body(), &single); err2 == nil && len(single.Checksums) > 0 {
			internalObj, err := registerCandidateToRecord(single, time.Now().UTC())
			if err != nil {
				return middleware.HandleError(c, err)
			}
			if err := s.objectService.RegisterObjects(c.Context(), []objects.Record{internalObj}); err != nil {
				return middleware.HandleError(c, err)
			}
			finalObj, err := s.objectService.GetObject(c.Context(), string(internalObj.Id), "read")
			if err != nil {
				return middleware.HandleError(c, err)
			}
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{
				"objects": []any{ObjectPayload(*finalObj)},
			})
		}
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	toRegister := make([]objects.Record, 0, len(body.Candidates))
	for _, cand := range body.Candidates {
		internalObj, err := registerCandidateToRecord(cand, time.Now().UTC())
		if err != nil {
			return middleware.HandleError(c, err)
		}
		toRegister = append(toRegister, internalObj)
	}

	if err := s.objectService.RegisterObjects(c.Context(), toRegister); err != nil {
		return middleware.HandleError(c, err)
	}

	registered := make([]any, len(toRegister))
	for i, internal := range toRegister {
		obj, err := s.objectService.GetObject(c.Context(), string(internal.Id), "read")
		if err != nil {
			return middleware.HandleError(c, err)
		}
		registered[i] = ObjectPayload(*obj)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"objects": registered})
}

type registerObjectsRequest struct {
	Candidates []registerObjectCandidate `json:"candidates"`
}

type registerObjectCandidate struct {
	generated.DrsObjectCandidate
}

func registerCandidateToRecord(c registerObjectCandidate, now time.Time) (objects.Record, error) {
	return objects.CandidateToRecord(FromGeneratedCandidate(c.DrsObjectCandidate), now)
}
