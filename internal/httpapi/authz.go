package httpapi

import (
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type AuthzOptions struct {
	Mode      string
	Evaluator access.Evaluator
}

// AuthorizationHandler installs the access session and applies evaluator
// decisions at the Fiber boundary.
func AuthorizationHandler(options AuthzOptions) fiber.Handler {
	mode := strings.ToLower(strings.TrimSpace(options.Mode))
	evaluator := options.Evaluator
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		authHeader := c.Get(fiber.HeaderAuthorization)
		session := access.NewSession(mode)
		if mode == "gen3" {
			session.AuthzEnforced = true
		}
		applySession := func(session *access.Session) error {
			c.SetContext(access.WithSession(ctx, session))
			return c.Next()
		}
		if isPublicDRSMetadataRequest(c) && strings.TrimSpace(authHeader) == "" {
			return applySession(session)
		}
		if evaluator == nil {
			return applySession(session)
		}
		result := evaluator.Evaluate(access.EvaluationRequest{
			Context:    ctx,
			RequestID:  requestid.GetRequestID(ctx),
			Mode:       mode,
			AuthHeader: authHeader,
			Method:     c.Method(),
			Path:       c.Path(),
		})
		if result.Session == nil {
			result.Session = session
		}
		switch result.Decision {
		case access.DecisionContinue:
			return applySession(result.Session)
		case access.DecisionUnauthorized:
			if result.BasicChallenge {
				c.Set(fiber.HeaderWWWAuthenticate, `Basic realm="syfon"`)
			}
			return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		case access.DecisionForbidden:
			return Reject(c, fiber.StatusForbidden, "Forbidden")
		case access.DecisionInternalError:
			return Reject(c, fiber.StatusInternalServerError, "Internal Server Error")
		default:
			return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
	}
}

// isPublicDRSMetadataRequest identifies the metadata endpoints that may be
// queried anonymously. Static mutation and access routes are excluded even
// when their final path segment resembles an object ID.
func isPublicDRSMetadataRequest(c fiber.Ctx) bool {
	if c == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(c.Method()))
	path := strings.TrimSuffix(strings.TrimSpace(c.Path()), "/")
	const drsPrefix = "/ga4gh/drs/v1"
	path = strings.TrimPrefix(path, drsPrefix)
	if !strings.HasPrefix(path, "/objects") {
		return false
	}
	if method == http.MethodPost && path == "/objects" {
		return true
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/objects/checksum/") {
		return strings.TrimPrefix(path, "/objects/checksum/") != ""
	}
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	objectID := strings.TrimPrefix(path, "/objects/")
	if objectID == "" || strings.Contains(objectID, "/") {
		return false
	}
	switch objectID {
	case "register", "access", "delete", "access-methods", "checksum":
		return false
	default:
		return true
	}
}
