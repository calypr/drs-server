package drs

import (
	"encoding/json"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func (s *server) RegisterObjects(c fiber.Ctx) error {
	var body generated.RegisterObjectsJSONBody
	var candidates []objects.Candidate
	if err := json.Unmarshal(c.Body(), &body); err == nil && len(body.Candidates) > 0 {
		candidates = make([]objects.Candidate, 0, len(body.Candidates))
		for _, candidate := range body.Candidates {
			candidates = append(candidates, FromGeneratedCandidate(candidate))
		}
	} else {
		var single generated.DrsObjectCandidate
		if err2 := json.Unmarshal(c.Body(), &single); err2 == nil && len(single.Checksums) > 0 {
			candidates = []objects.Candidate{FromGeneratedCandidate(single)}
		} else {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	registered, err := s.objectService.RegisterCandidates(c.Context(), candidates)
	if err != nil {
		return middleware.HandleError(c, err)
	}

	response := make([]ObjectResponse, len(registered))
	for i, record := range registered {
		response[i] = ObjectPayload(record)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"objects": response})
}
