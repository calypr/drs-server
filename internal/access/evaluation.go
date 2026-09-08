package access

import "context"

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
	Session        *Session
	Decision       Decision
	BasicChallenge bool
}

type Evaluator interface {
	Evaluate(EvaluationRequest) EvaluationResult
}
