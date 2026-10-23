package model

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("resource not found")
var ErrUnauthenticated = errors.New("unauthenticated")
var ErrInvalidInput = errors.New("invalid input")
var ErrConflict = errors.New("state conflict")
var ErrRunConflict = fmt.Errorf("run conflict: %w", ErrConflict)
var ErrIdempotencyConflict = errors.New("idempotency conflict")
var ErrQuota = errors.New("run quota exceeded")
var ErrModelUnavailable = errors.New("model unavailable")
var ErrAgentUnavailable = errors.New("agent unavailable")
var ErrCheckpointIncompatible = errors.New("checkpoint incompatible")
var ErrTravelOutcomeUnknown = fmt.Errorf("travel operation outcome requires verification: %w", ErrConflict)
var ErrTravelEvidenceExpired = fmt.Errorf("stored travel evidence expired: %w", ErrConflict)
var ErrProviderTransient = errors.New("temporary provider failure")

type TransitionRunInput struct {
	Scope               Scope
	RunID               string
	Attempt             int64
	From, To, ErrorCode string
}

type AdmitRunInput struct {
	Scope                                                Scope
	ConversationID, Content, IdempotencyKey, RequestHash string
}
type FinishRunInput struct {
	Scope     Scope
	RunID     string
	Attempt   int64
	FinalText string
}
type FinalizePauseInput struct {
	Scope   Scope
	RunID   string
	Attempt int64
}
