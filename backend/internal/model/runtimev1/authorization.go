// Package runtimev1 contains the new runtime's transport and domain values.
// Decode external values with runtimeprotocol before using them; types alone do
// not establish schema validity, ownership, authorization or execution rights.
package runtimev1

import (
	"bytes"
	"encoding/json"
	"time"
)

// Field preserves absence separately from explicit null. Use json omitzero on
// optional fields; required nullable fields instead use pointers without omit.
type Field[T any] struct {
	Value         T
	Present, Null bool
}

func (f Field[T]) IsZero() bool { return !f.Present }
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.Null || !f.Present {
		return []byte("null"), nil
	}
	return json.Marshal(f.Value)
}
func (f *Field[T]) UnmarshalJSON(raw []byte) error {
	var value T
	null := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	if !null {
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
	}
	*f = Field[T]{Value: value, Present: true, Null: null}
	return nil
}

type LeaseRef struct {
	WorkerID  string
	Epoch     int64
	ExpiresAt time.Time
}
type ExecRef struct {
	ScopeID, ScopeHash, OwnerKind, OwnerID, ThreadID string
	Lease                                            LeaseRef
}
type RuntimeScope struct {
	UserID       string `json:"user_id"`
	MainThreadID string `json:"main_thread_id"`
	RunID        string `json:"run_id"`
	ToolCallID   string `json:"tool_call_id"`
	OperationID  string `json:"operation_id"`
	Attempt      int64  `json:"attempt"`
	Deadline     string `json:"deadline"`
}
type TrustedTravelMeta struct {
	SchemaVersion      int64  `json:"schema_version"`
	ScopeID            string `json:"scope_id"`
	ScopeHash          string `json:"scope_hash"`
	SubjectID          string `json:"subject_id"`
	ConversationID     string `json:"conversation_id"`
	SourceRunID        string `json:"source_run_id"`
	DelegationID       string `json:"delegation_id"`
	SourceContextEpoch int64  `json:"source_context_epoch"`
	DeadlineAt         string `json:"deadline_at"`
}
