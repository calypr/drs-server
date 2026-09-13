package authentication

import (
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/plugin"
)

// Evaluate composes configured authentication mechanisms without depending on Fiber.
func (r *Runtime) Evaluate(req access.EvaluationRequest) access.EvaluationResult {
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

func (r *Runtime) evaluateLocal(req access.EvaluationRequest, session *access.Session) access.EvaluationResult {
	if r.localAuthzError != nil {
		return access.EvaluationResult{Session: session, Decision: access.DecisionInternalError}
	}
	if r.authentication == nil {
		return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
	}

	output, err := r.authentication.Authenticate(req.Context, &plugin.AuthenticationInput{
		RequestID:  req.RequestID,
		AuthHeader: req.AuthHeader,
		Metadata:   map[string]interface{}{},
	})
	if err != nil || !output.Authenticated {
		return access.EvaluationResult{Session: session, Decision: access.DecisionUnauthorized, BasicChallenge: true}
	}
	session.SetSubject(output.Subject)
	session.SetClaims(output.Claims)
	session.SetSource(access.SourceLocalBasic)
	if resources, privileges, ok := authorizationFromClaims(output.Claims); ok {
		session.SetAuthorizations(resources, privileges, true)
		session.SetSource(access.SourceLocalCSV)
		return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
	}
	if r.localAuthzForSubject != nil && output.Subject != "" {
		if resources, privileges, ok := r.localAuthzForSubject(output.Subject); ok {
			session.SetAuthorizations(resources, privileges, true)
			session.SetSource(access.SourceLocalCSV)
		} else {
			return access.EvaluationResult{Session: session, Decision: access.DecisionForbidden}
		}
	} else if r.localAuthzForSubject != nil {
		return access.EvaluationResult{Session: session, Decision: access.DecisionForbidden}
	}
	return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
}

func (r *Runtime) evaluateGen3(req access.EvaluationRequest, session *access.Session) access.EvaluationResult {
	if r.mock.Enabled {
		if r.mock.RequireAuthHeader && !session.AuthHeaderPresent {
			return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
		}
		session.AuthHeaderPresent = true
		session.AuthzEnforced = true
		session.SetSource(access.SourceGen3Mock)
		resources, privileges := mockAuthorizations(r.mock)
		session.SetAuthorizations(resources, privileges, true)
		return r.authorize(req, session)
	}
	if strings.TrimSpace(req.AuthHeader) == "" {
		return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
	}

	var (
		output *plugin.AuthenticationOutput
		err    error
	)
	if r.authentication == nil {
		if r.authorization != nil {
			output = &plugin.AuthenticationOutput{Authenticated: true}
		} else {
			return access.EvaluationResult{Session: session, Decision: access.DecisionUnauthorized}
		}
	} else {
		output, err = r.authentication.Authenticate(req.Context, &plugin.AuthenticationInput{
			RequestID:  req.RequestID,
			AuthHeader: req.AuthHeader,
			Metadata:   map[string]interface{}{},
		})
		if err != nil {
			r.logger.Debug("authentication failed", "request_id", req.RequestID, "authenticated", false)
			return access.EvaluationResult{Session: session, Decision: access.DecisionUnauthorized}
		}
		r.logger.Debug(
			"authentication plugin output",
			"request_id", req.RequestID,
			"authenticated", output.Authenticated,
			"claim_count", len(output.Claims),
		)
	}
	if output == nil || !output.Authenticated {
		return access.EvaluationResult{Session: session, Decision: access.DecisionUnauthorized}
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

func (r *Runtime) authorize(req access.EvaluationRequest, session *access.Session) access.EvaluationResult {
	if r.authorization == nil {
		return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
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
		return access.EvaluationResult{Session: session, Decision: access.DecisionUnauthorized}
	}
	if !authzOutput.Allow {
		return access.EvaluationResult{Session: session, Decision: access.DecisionForbidden}
	}
	return access.EvaluationResult{Session: session, Decision: access.DecisionContinue}
}

func mockAuthorizations(config config.MockAuthConfig) ([]string, map[string]map[string]bool) {
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
