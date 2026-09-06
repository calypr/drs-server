package authentication

import (
	"context"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/plugin"
)

// EvaluationRequest is the framework-neutral input to request authentication.
type EvaluationRequest struct {
	Context    context.Context
	RequestID  string
	Mode       string
	AuthHeader string
	Method     string
	Path       string
}

type Decision uint8

const (
	DecisionContinue Decision = iota
	DecisionUnauthorized
	DecisionForbidden
	DecisionInternalError
)

// EvaluationResult contains the session state and semantic outcome for a request.
type EvaluationResult struct {
	Session        *access.Session
	Decision       Decision
	BasicChallenge bool
}

// Evaluate composes configured authentication mechanisms without depending on Fiber.
func (r *Runtime) Evaluate(req EvaluationRequest) EvaluationResult {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	session := access.NewSession(mode)
	if mode == "gen3" {
		session.AuthHeaderPresent = strings.TrimSpace(req.AuthHeader) != ""
		session.AuthzEnforced = true
	}

	if mode == "gen3" {
		return r.evaluateGen3(req, session)
	}
	return r.evaluateLocal(req, session)
}

func (r *Runtime) evaluateLocal(req EvaluationRequest, session *access.Session) EvaluationResult {
	if r.localAuthzError != nil {
		return EvaluationResult{Session: session, Decision: DecisionInternalError}
	}
	if r.authentication == nil {
		return EvaluationResult{Session: session, Decision: DecisionContinue}
	}

	output, err := r.authentication.Authenticate(req.Context, &plugin.AuthenticationInput{
		RequestID:  req.RequestID,
		AuthHeader: req.AuthHeader,
		Metadata:   map[string]interface{}{},
	})
	if err != nil || !output.Authenticated {
		return EvaluationResult{Session: session, Decision: DecisionUnauthorized, BasicChallenge: true}
	}
	session.SetSubject(output.Subject)
	session.SetClaims(output.Claims)
	session.SetSource(access.SourceLocalBasic)
	if resources, privileges, ok := authorizationFromClaims(output.Claims); ok {
		session.SetAuthorizations(resources, privileges, true)
		session.SetSource(access.SourceLocalCSV)
		return EvaluationResult{Session: session, Decision: DecisionContinue}
	}
	if r.localAuthzForSubject != nil && output.Subject != "" {
		if resources, privileges, ok := r.localAuthzForSubject(output.Subject); ok {
			session.SetAuthorizations(resources, privileges, true)
			session.SetSource(access.SourceLocalCSV)
		} else {
			return EvaluationResult{Session: session, Decision: DecisionForbidden}
		}
	} else if r.localAuthzForSubject != nil {
		return EvaluationResult{Session: session, Decision: DecisionForbidden}
	}
	return EvaluationResult{Session: session, Decision: DecisionContinue}
}

func (r *Runtime) evaluateGen3(req EvaluationRequest, session *access.Session) EvaluationResult {
	if r.mock.Enabled {
		if r.mock.RequireAuthHeader && !session.AuthHeaderPresent {
			return EvaluationResult{Session: session, Decision: DecisionContinue}
		}
		session.AuthHeaderPresent = true
		session.AuthzEnforced = true
		session.SetSource(access.SourceGen3Mock)
		resources, privileges := mockAuthorizations(r.mock)
		session.SetAuthorizations(resources, privileges, true)
		return r.authorize(req, session)
	}
	if strings.TrimSpace(req.AuthHeader) == "" {
		return EvaluationResult{Session: session, Decision: DecisionContinue}
	}

	var (
		output *plugin.AuthenticationOutput
		err    error
	)
	if r.authentication == nil {
		if r.authorization != nil {
			output = &plugin.AuthenticationOutput{Authenticated: true}
		} else {
			return EvaluationResult{Session: session, Decision: DecisionUnauthorized}
		}
	} else {
		output, err = r.authentication.Authenticate(req.Context, &plugin.AuthenticationInput{
			RequestID:  req.RequestID,
			AuthHeader: req.AuthHeader,
			Metadata:   map[string]interface{}{},
		})
		if err != nil {
			r.logger.Debug("authentication failed", "error", err)
			return EvaluationResult{Session: session, Decision: DecisionUnauthorized}
		}
		r.logger.Debug("authentication plugin output", "authenticated", output.Authenticated, "subject", output.Subject, "claims", output.Claims, "reason", output.Reason)
	}
	if output == nil || !output.Authenticated {
		return EvaluationResult{Session: session, Decision: DecisionUnauthorized}
	}
	session.SetSubject(output.Subject)
	session.SetClaims(output.Claims)
	session.SetSource(access.SourceGen3Fence)

	tokenString, err := extractBearerLikeToken(req.AuthHeader)
	if err != nil {
		r.logger.Debug("failed to extract bearer token for authorization lookup", "error", err)
	} else if r.tokenResolver != nil {
		authResult := r.tokenResolver.Resolve(req.Context, tokenString)
		if authResult.Negative {
			r.logger.Debug("authorization lookup failed or returned no usable privileges")
		} else {
			r.logger.Debug("authorization lookup complete", "resources", len(authResult.Resources))
			session.SetAuthorizations(authResult.Resources, authResult.Privileges, true)
		}
	}
	return r.authorize(req, session)
}

func (r *Runtime) authorize(req EvaluationRequest, session *access.Session) EvaluationResult {
	if r.authorization == nil {
		return EvaluationResult{Session: session, Decision: DecisionContinue}
	}
	authzOutput, err := r.authorization.Authorize(req.Context, &plugin.AuthorizationInput{
		RequestID: req.RequestID,
		Subject:   session.Subject,
		Action:    req.Method,
		Resource:  req.Path,
		Claims:    session.Claims,
		Metadata:  map[string]interface{}{},
	})
	if err != nil {
		return EvaluationResult{Session: session, Decision: DecisionUnauthorized}
	}
	if !authzOutput.Allow {
		return EvaluationResult{Session: session, Decision: DecisionForbidden}
	}
	return EvaluationResult{Session: session, Decision: DecisionContinue}
}

func mockAuthorizations(config mockConfig) ([]string, map[string]map[string]bool) {
	resources := append([]string(nil), config.Resources...)
	privileges := make(map[string]map[string]bool, len(resources))
	for _, resource := range resources {
		methods := make(map[string]bool, len(config.Methods))
		for _, method := range config.Methods {
			methods[method] = true
		}
		privileges[resource] = methods
	}
	return resources, privileges
}
