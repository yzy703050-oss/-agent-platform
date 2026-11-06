package runtimev1

type TravelTarget struct {
	PlanID           string `json:"plan_id"`
	TravelThreadID   string `json:"travel_thread_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type TravelToolArgs struct {
	Mode    string              `json:"mode"`
	Target  Field[TravelTarget] `json:"target,omitzero"`
	Request string              `json:"request"`
	Context ContextSummary      `json:"context"`
}
type DelegationInput struct {
	SchemaVersion int64          `json:"schema_version"`
	Scope         RuntimeScope   `json:"scope"`
	Args          TravelToolArgs `json:"args"`
}
type DispatchCommand struct {
	SchemaVersion int64  `json:"schema_version"`
	MessageID     string `json:"message_id"`
	SourceRunID   string `json:"source_run_id"`
	DelegationID  string `json:"delegation_id"`
	DispatchSeq   int64  `json:"dispatch_seq"`
	CreatedAt     string `json:"created_at"`
}
type TravelTaskSubmit struct {
	SchemaVersion int64          `json:"schema_version"`
	RequestKey    string         `json:"request_key"`
	HashVersion   string         `json:"hash_version"`
	InputHash     string         `json:"input_hash"`
	Args          TravelToolArgs `json:"args"`
}
type TravelTaskLookup struct {
	SchemaVersion int64  `json:"schema_version"`
	TaskID        string `json:"task_id"`
}
type TravelTaskFind struct {
	SchemaVersion int64  `json:"schema_version"`
	RequestKey    string `json:"request_key"`
	InputHash     string `json:"input_hash"`
}
type TravelTaskReceipt struct {
	SchemaVersion int64  `json:"schema_version"`
	TaskID        string `json:"task_id"`
	RequestKey    string `json:"request_key"`
	InputHash     string `json:"input_hash"`
	Status        string `json:"status"`
	StateVersion  int64  `json:"state_version"`
	DeadlineAt    string `json:"deadline_at"`
}
type TravelTaskResult struct {
	SchemaVersion int64            `json:"schema_version"`
	TaskID        string           `json:"task_id"`
	RequestKey    string           `json:"request_key"`
	InputHash     string           `json:"input_hash"`
	Status        string           `json:"status"`
	ResultVersion int64            `json:"result_version"`
	CompletedAt   string           `json:"completed_at"`
	Result        TravelCallResult `json:"result"`
}
type TravelCallResult struct {
	SchemaVersion int64               `json:"schema_version"`
	CallStatus    string              `json:"call_status"`
	TaskOutcome   string              `json:"task_outcome"`
	Plan          *PlanDirectoryEntry `json:"plan"`
	Summary       string              `json:"summary"`
	Facts         []ContextFact       `json:"facts"`
	Selections    []Selection         `json:"selections"`
	Unresolved    []string            `json:"unresolved"`
	Commits       []CommitResult      `json:"commits"`
	Error         *ErrorDetail        `json:"error"`
}
