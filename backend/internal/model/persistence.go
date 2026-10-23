package model

import "errors"

var ErrNotFound = errors.New("resource not found")
var ErrUnauthenticated = errors.New("unauthenticated")
var ErrInvalidInput = errors.New("invalid input")
var ErrConflict = errors.New("state conflict")
var ErrIdempotencyConflict = errors.New("idempotency conflict")
var ErrQuota = errors.New("run quota exceeded")
var ErrModelUnavailable = errors.New("model unavailable")
var ErrAgentUnavailable = errors.New("agent unavailable")

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
