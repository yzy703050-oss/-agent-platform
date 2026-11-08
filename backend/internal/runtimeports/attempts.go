// Package runtimeports carries program-injected execution identity and durable
// physical-call accounting. Model arguments never construct these contexts.
package runtimeports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model/runtimev1"
)

var ErrInFlight = errors.New("original physical call may still be in flight")
var ErrRetryExhausted = errors.New("four physical attempts exhausted")
var ErrInputConflict = errors.New("logical call input changed")

type Execution struct {
	Scope                                                         control.AuthorizedScope
	Limits                                                        runtimev1.LimitSnapshot
	Fingerprint                                                   string
	Service, UserID, ConversationID, RunID, ThreadID, OperationID string
	ContextEpoch                                                  int64
}

type AttemptRecord struct {
	control.Attempt
	InputHash        string     `json:"input_hash"`
	OwnerJobID       string     `json:"owner_job_id"`
	OwnerOperationID string     `json:"owner_operation_id"`
	Epoch            int64      `json:"lease_epoch"`
	NextRetryAt      *time.Time `json:"next_retry_at"`
}

type CallState struct {
	Execution Execution
	Attempts  []AttemptRecord
}

type AttemptStore interface {
	LoadCall(context.Context, runtimev1.ExecRef, string, string, string) (CallState, error)
	Reserve(context.Context, runtimev1.ExecRef, control.AttemptInput) (control.Attempt, error)
	Settle(context.Context, runtimev1.ExecRef, control.Settlement) error
	ScheduleRetry(context.Context, runtimev1.ExecRef, string, time.Time) (time.Time, error)
	FindModelOutput(context.Context, runtimev1.ExecRef, string) (json.RawMessage, bool, error)
	SaveModelOutput(context.Context, runtimev1.ExecRef, string, string, json.RawMessage) error
}

type Call struct {
	Ref        runtimev1.ExecRef
	LogicalKey string
	AttemptNo  int
	Execution  *Execution
}
type callKey struct{}

func WithCall(ctx context.Context, ref runtimev1.ExecRef, key string, attempt int) context.Context {
	return context.WithValue(ctx, callKey{}, Call{Ref: ref, LogicalKey: key, AttemptNo: attempt})
}
func CurrentCall(ctx context.Context) (Call, bool) {
	v, ok := ctx.Value(callKey{}).(Call)
	return v, ok
}
func WithExecution(ctx context.Context, execution Execution) context.Context {
	call, _ := CurrentCall(ctx)
	call.Execution = &execution
	return context.WithValue(ctx, callKey{}, call)
}

func AttemptID(ref runtimev1.ExecRef, key string, number int) string {
	b, _ := json.Marshal([]any{ref.ScopeID, key, number})
	sum := sha256.Sum256(b)
	return "attempt:" + hex.EncodeToString(sum[:])
}
