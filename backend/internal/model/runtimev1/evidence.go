package runtimev1

import "encoding/json"

type EvidenceRef struct {
	EvidenceID     string `json:"evidence_id"`
	ResultRevision int64  `json:"result_revision"`
	CandidateID    string `json:"candidate_id"`
}
type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Basis       string `json:"basis"`
}
type Candidate struct {
	CandidateID   string                     `json:"candidate_id"`
	Title         string                     `json:"title"`
	Price         *Money                     `json:"price"`
	Attributes    map[string]json.RawMessage `json:"attributes"`
	SourceURL     *string                    `json:"source_url"`
	MissingFields []string                   `json:"missing_fields"`
}
type ErrorDetail struct {
	Code           string  `json:"code"`
	Category       string  `json:"category"`
	Message        string  `json:"message"`
	Path           *string `json:"path"`
	RetryExhausted bool    `json:"retry_exhausted"`
	ModelAction    string  `json:"model_action"`
}
type ExecutionRequest struct {
	SchemaVersion   int64                      `json:"schema_version"`
	Scope           RuntimeScope               `json:"scope"`
	PlanID          string                     `json:"plan_id"`
	PlanRevision    int64                      `json:"plan_revision"`
	TaskID          string                     `json:"task_id"`
	TaskRevision    int64                      `json:"task_revision"`
	ItemID          string                     `json:"item_id"`
	ExecutionStepID string                     `json:"execution_step_id"`
	Generation      int64                      `json:"generation"`
	Domain          string                     `json:"domain"`
	Input           map[string]json.RawMessage `json:"input"`
	Requirements    []Requirement              `json:"requirements"`
	Ranking         []string                   `json:"ranking"`
	InputHash       string                     `json:"input_hash"`
}
type ExecutionResult struct {
	SchemaVersion   int64        `json:"schema_version"`
	ExecutionStepID string       `json:"execution_step_id"`
	PlanID          string       `json:"plan_id"`
	TaskID          string       `json:"task_id"`
	TaskRevision    int64        `json:"task_revision"`
	ItemID          string       `json:"item_id"`
	Generation      int64        `json:"generation"`
	Status          string       `json:"status"`
	Coverage        string       `json:"coverage"`
	EvidenceID      *string      `json:"evidence_id"`
	ResultRevision  *int64       `json:"result_revision"`
	RetrievedAt     *string      `json:"retrieved_at"`
	ValidUntil      *string      `json:"valid_until"`
	Candidates      []Candidate  `json:"candidates"`
	MissingFields   []string     `json:"missing_fields"`
	Error           *ErrorDetail `json:"error"`
}
type ExecutionReport struct {
	PlanID       string            `json:"plan_id"`
	PlanRevision int64             `json:"plan_revision"`
	Results      []ExecutionResult `json:"results"`
	Unresolved   []string          `json:"unresolved"`
}
type EvidenceIdentity struct {
	EvidenceID     string `json:"evidence_id"`
	ResultRevision int64  `json:"result_revision"`
}
type SharedToolCallRecord struct {
	ID             string                     `json:"id"`
	UserID         string                     `json:"user_id"`
	ConversationID string                     `json:"conversation_id"`
	RunID          string                     `json:"run_id"`
	ThreadID       string                     `json:"thread_id"`
	CallKey        string                     `json:"call_key"`
	ToolName       string                     `json:"tool_name"`
	Domain         string                     `json:"domain"`
	Provider       string                     `json:"provider"`
	InputHash      string                     `json:"input_hash"`
	SchemaVersion  int64                      `json:"schema_version"`
	AgentKind      string                     `json:"agent_kind"`
	ThreadRole     string                     `json:"thread_role"`
	Mode           string                     `json:"mode"`
	Arguments      map[string]json.RawMessage `json:"arguments"`
	SourceRefs     []SourceRef                `json:"source_refs"`
	Status         string                     `json:"status"`
	Coverage       string                     `json:"coverage"`
	ResultRefs     []EvidenceIdentity         `json:"result_refs"`
	Error          *ErrorDetail               `json:"error"`
	CreatedAt      string                     `json:"created_at"`
	ContextEpoch   int64                      `json:"context_epoch"`
}
type SharedToolEvidenceRecord struct {
	ID               string                     `json:"id"`
	UserID           string                     `json:"user_id"`
	ConversationID   string                     `json:"conversation_id"`
	ToolCallRecordID string                     `json:"tool_call_record_id"`
	Domain           string                     `json:"domain"`
	Provider         string                     `json:"provider"`
	SchemaVersion    int64                      `json:"schema_version"`
	ResultRevision   int64                      `json:"result_revision"`
	Mode             string                     `json:"mode"`
	Snapshot         map[string]json.RawMessage `json:"snapshot"`
	SourceRefs       []SourceRef                `json:"source_refs"`
	FetchedAt        string                     `json:"fetched_at"`
	ExpiresAt        *string                    `json:"expires_at"`
}
