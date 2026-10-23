package model

import "errors"

var ErrNotFound = errors.New("resource not found")
var ErrConflict = errors.New("state conflict")
var ErrIdempotencyConflict = errors.New("idempotency conflict")

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
