package runtimev1

type SourceRef struct {
	Kind     string       `json:"kind"`
	ID       string       `json:"id"`
	Revision Field[int64] `json:"revision,omitzero"`
}
type ContextFact struct {
	Text    string      `json:"text"`
	Sources []SourceRef `json:"sources"`
}
type ContextSummary struct {
	Text             string      `json:"text"`
	Sources          []SourceRef `json:"sources"`
	ThroughMessageID *string     `json:"through_message_id"`
}
type Turn struct {
	RunID         string `json:"run_id"`
	UserMessageID string `json:"user_message_id"`
	UserText      string `json:"user_text"`
	AssistantText string `json:"assistant_text"`
}
type WorkingMemory struct {
	Summary      ContextSummary  `json:"summary"`
	RecentTurns  []Turn          `json:"recent_turns"`
	PriorReplans []ReplanHistory `json:"prior_replans"`
}
type MemoryReviewDecision struct {
	Needed           bool     `json:"needed"`
	Reason           string   `json:"reason"`
	SourceMessageIDs []string `json:"source_message_ids"`
}
type MemorySubmitArgs struct {
	Reason           string   `json:"reason"`
	SourceMessageIDs []string `json:"source_message_ids"`
}
type MemoryTaskReceipt struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Saved  bool   `json:"saved"`
}
type MemoryRecord struct {
	MemoryID  string      `json:"memory_id"`
	Revision  int64       `json:"revision"`
	Domain    string      `json:"domain"`
	Text      string      `json:"text"`
	Authority string      `json:"authority"`
	Status    string      `json:"status"`
	Sources   []SourceRef `json:"sources"`
}
type MemoryMessage struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
}
type MemoryAgentInput struct {
	SchemaVersion      int64           `json:"schema_version"`
	TaskID             string          `json:"task_id"`
	UserID             string          `json:"user_id"`
	MainThreadID       string          `json:"main_thread_id"`
	Trigger            string          `json:"trigger"`
	FromRound          int64           `json:"from_round"`
	ThroughRound       int64           `json:"through_round"`
	BaseMemoryRevision int64           `json:"base_memory_revision"`
	Messages           []MemoryMessage `json:"messages"`
	ExistingMemories   []MemoryRecord  `json:"existing_memories"`
}
type MemoryUpdate struct {
	Op               string        `json:"op"`
	MemoryID         Field[string] `json:"memory_id,omitzero"`
	ExpectedRevision Field[int64]  `json:"expected_revision,omitzero"`
	Domain           Field[string] `json:"domain,omitzero"`
	Text             Field[string] `json:"text,omitzero"`
	Sources          []SourceRef   `json:"sources"`
}
type MemoryProposal struct {
	SchemaVersion      int64          `json:"schema_version"`
	TaskID             string         `json:"task_id"`
	BaseMemoryRevision int64          `json:"base_memory_revision"`
	Updates            []MemoryUpdate `json:"updates"`
}
type MemoryMaintenanceResult struct {
	TaskID                 string       `json:"task_id"`
	Status                 string       `json:"status"`
	UpdatesApplied         int64        `json:"updates_applied"`
	MemoryRevision         int64        `json:"memory_revision"`
	ReviewedThroughRound   int64        `json:"reviewed_through_round"`
	SummarizedThroughRound int64        `json:"summarized_through_round"`
	Error                  *ErrorDetail `json:"error"`
}
type ContextRequest struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}
type ShortTermContext struct {
	Summary     ContextSummary `json:"summary"`
	RecentTurns []Turn         `json:"recent_turns"`
}
type MemoryReviewState struct {
	Interval               int64 `json:"interval"`
	NewEffectiveRounds     int64 `json:"new_effective_rounds"`
	ReviewDue              bool  `json:"review_due"`
	ReviewedThroughRound   int64 `json:"reviewed_through_round"`
	SummarizedThroughRound int64 `json:"summarized_through_round"`
}
type GeneralContext struct {
	SchemaVersion    int64                `json:"schema_version"`
	Request          ContextRequest       `json:"request"`
	ShortTerm        ShortTermContext     `json:"short_term"`
	LongTermMemories []MemoryRecord       `json:"long_term_memories"`
	RelatedPlans     []PlanDirectoryEntry `json:"related_plans"`
	MemoryReview     MemoryReviewState    `json:"memory_review"`
}
type MemoryWriteArgs struct {
	Update MemoryUpdate `json:"update"`
}
type MemoryWriteResult struct {
	OperationID    string `json:"operation_id"`
	Status         string `json:"status"`
	MemoryRevision int64  `json:"memory_revision"`
	UpdatesApplied int64  `json:"updates_applied"`
}
type UserMemorySnapshot struct {
	SchemaVersion int64          `json:"schema_version"`
	Memories      []MemoryRecord `json:"memories"`
}
