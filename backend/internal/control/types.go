// Package control is the transaction-bound client for private authorization and
// call-accounting functions. It owns no database pool or business-schema access.
package control

import (
	"encoding/json"
	"time"

	"agent-platform/backend/internal/model/runtimev1"
)

type Reservation struct {
	ModelAttempts   int64 `json:"model_attempts"`
	ToolAttempts    int64 `json:"tool_attempts"`
	TokenUpperBound int64 `json:"token_upper_bound"`
	CostUpperMicros int64 `json:"cost_upper_micros"`
}

type ScopeInput struct {
	ScopeID        string          `json:"scope_id"`
	OwnerService   string          `json:"owner_service"`
	SubjectID      string          `json:"subject_id"`
	SourceRunID    string          `json:"source_run_id,omitempty"`
	ConversationID string          `json:"conversation_id"`
	DelegationID   string          `json:"delegation_id,omitempty"`
	OperationID    string          `json:"operation_id,omitempty"`
	Capabilities   []string        `json:"capabilities"`
	Targets        []string        `json:"targets"`
	SourceScope    json.RawMessage `json:"source_scope"`
	ContextEpoch   int64           `json:"context_epoch"`
	ExpiresAt      time.Time       `json:"expires_at"`
	BudgetLimits   Reservation     `json:"budget_limits"`
}

type AuthorizedScope struct {
	ScopeInput
	IssuerService string `json:"issuer_service"`
	ScopeHash     string `json:"scope_hash"`
}

type AttemptInput struct {
	Ref                                       runtimev1.ExecRef
	AttemptID, LogicalKey, Capability, Target string
	AttemptNo                                 int
	ResourceKeys                              []string
	Reservation                               Reservation
	HoldUntil                                 time.Time
}

type Attempt struct {
	AttemptID   string      `json:"attempt_id"`
	ScopeID     string      `json:"scope_id"`
	LogicalKey  string      `json:"logical_call_key"`
	AttemptNo   int         `json:"attempt_no"`
	Status      string      `json:"status"`
	Reservation Reservation `json:"reservation"`
	HoldUntil   time.Time   `json:"hold_until"`
	Replayed    bool        `json:"replayed"`
}

type Settlement struct {
	AttemptID   string          `json:"attempt_id"`
	Usage       *Reservation    `json:"usage"`
	Status      string          `json:"status"`
	ResponseRef json.RawMessage `json:"response_ref"`
}
