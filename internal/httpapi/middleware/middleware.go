package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type authenticationEvaluator interface {
	Evaluate(authentication.EvaluationRequest) authentication.EvaluationResult
}

type Options struct {
	Mode      string
	Evaluator authenticationEvaluator
}

type AuthzMiddleware struct {
	logger    *slog.Logger
	mode      string
	evaluator authenticationEvaluator
}

func NewAuthzMiddleware(logger *slog.Logger, options Options) *AuthzMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthzMiddleware{
		logger:    logger,
		mode:      strings.ToLower(strings.TrimSpace(options.Mode)),
		evaluator: options.Evaluator,
	}
}

// FiberMiddleware returns the request-wiring handler for access context and decisions.
func (m *AuthzMiddleware) FiberMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		authHeader := c.Get(fiber.HeaderAuthorization)
		session := m.newSession()
		if isPublicDRSMetadataRequest(c) && strings.TrimSpace(authHeader) == "" {
			return m.applySession(c, ctx, session)
		}
		if m.evaluator == nil {
			return m.applySession(c, ctx, session)
		}
		result := m.evaluator.Evaluate(authentication.EvaluationRequest{
			Context:    ctx,
			RequestID:  requestid.GetRequestID(ctx),
			Mode:       m.mode,
			AuthHeader: authHeader,
			Method:     c.Method(),
			Path:       c.Path(),
		})
		return m.applyResult(c, ctx, session, result)
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
	if strings.HasPrefix(path, drsPrefix) {
		path = strings.TrimPrefix(path, drsPrefix)
	}
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

func (m *AuthzMiddleware) newSession() *access.Session {
	session := access.NewSession(m.mode)
	if m.mode == "gen3" {
		session.AuthzEnforced = true
	}
	return session
}

func (m *AuthzMiddleware) applySession(c fiber.Ctx, ctx context.Context, session *access.Session) error {
	ctx = access.WithSession(ctx, session)
	c.SetContext(ctx)
	return c.Next()
}

func (m *AuthzMiddleware) applyResult(c fiber.Ctx, ctx context.Context, fallback *access.Session, result authentication.EvaluationResult) error {
	session := result.Session
	if session == nil {
		session = fallback
	}
	switch result.Decision {
	case authentication.DecisionContinue:
		return m.applySession(c, ctx, session)
	case authentication.DecisionUnauthorized:
		if result.BasicChallenge {
			c.Set(fiber.HeaderWWWAuthenticate, `Basic realm="syfon"`)
		}
		return c.SendStatus(fiber.StatusUnauthorized)
	case authentication.DecisionForbidden:
		return c.SendStatus(fiber.StatusForbidden)
	case authentication.DecisionInternalError:
		return c.SendStatus(fiber.StatusInternalServerError)
	default:
		return c.SendStatus(fiber.StatusUnauthorized)
	}
}
