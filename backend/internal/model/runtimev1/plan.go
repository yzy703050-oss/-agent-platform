package runtimev1

import "encoding/json"

type Requirement struct {
	RequirementID string      `json:"requirement_id"`
	Text          string      `json:"text"`
	Origin        string      `json:"origin"`
	Scope         string      `json:"scope"`
	TaskIDs       []string    `json:"task_ids"`
	Sources       []SourceRef `json:"sources"`
}
type Stop struct {
	StopID        string        `json:"stop_id"`
	City          string        `json:"city"`
	ArrivalDate   Field[string] `json:"arrival_date,omitzero"`
	DepartureDate Field[string] `json:"departure_date,omitzero"`
}
type Leg struct {
	LegID         string        `json:"leg_id"`
	FromStopID    string        `json:"from_stop_id"`
	ToStopID      string        `json:"to_stop_id"`
	DepartureDate Field[string] `json:"departure_date,omitzero"`
}
type TaskTarget struct {
	Kind string        `json:"kind"`
	ID   Field[string] `json:"id,omitzero"`
}
type HotelParameters struct {
	City     Field[string]   `json:"city,omitzero"`
	CheckIn  Field[string]   `json:"check_in,omitzero"`
	CheckOut Field[string]   `json:"check_out,omitzero"`
	Guests   Field[int64]    `json:"guests,omitzero"`
	Keywords Field[[]string] `json:"keywords,omitzero"`
}
type TrainParameters struct {
	Origin        Field[string]   `json:"origin,omitzero"`
	Destination   Field[string]   `json:"destination,omitzero"`
	DepartureDate Field[string]   `json:"departure_date,omitzero"`
	Passengers    Field[int64]    `json:"passengers,omitzero"`
	Keywords      Field[[]string] `json:"keywords,omitzero"`
}
type WebParameters struct {
	Query     Field[string] `json:"query,omitzero"`
	TimeRange Field[string] `json:"time_range,omitzero"`
}
type WeatherParameters struct {
	City Field[string] `json:"city,omitzero"`
	Date Field[string] `json:"date,omitzero"`
}
type TaskParameters struct {
	Hotel   Field[HotelParameters]   `json:"hotel,omitzero"`
	Train   Field[TrainParameters]   `json:"train,omitzero"`
	Web     Field[WebParameters]     `json:"web,omitzero"`
	Weather Field[WeatherParameters] `json:"weather,omitzero"`
}
type QueryItem struct {
	ItemID    string   `json:"item_id"`
	Domain    string   `json:"domain"`
	DependsOn []string `json:"depends_on"`
}
type TaskDefinition struct {
	TaskID         string         `json:"task_id"`
	Title          string         `json:"title"`
	Target         TaskTarget     `json:"target"`
	Parameters     TaskParameters `json:"parameters"`
	RequirementIDs []string       `json:"requirement_ids"`
	Ranking        []string       `json:"ranking"`
	Items          []QueryItem    `json:"items"`
}
type PlanDefinition struct {
	Title        string           `json:"title"`
	Goal         string           `json:"goal"`
	Purpose      string           `json:"purpose"`
	Timezone     string           `json:"timezone"`
	Stops        []Stop           `json:"stops"`
	Legs         []Leg            `json:"legs"`
	Requirements []Requirement    `json:"requirements"`
	Tasks        []TaskDefinition `json:"tasks"`
}
type Selection struct {
	SelectionID  string      `json:"selection_id"`
	TaskID       string      `json:"task_id"`
	TaskRevision int64       `json:"task_revision"`
	ItemID       string      `json:"item_id"`
	Evidence     EvidenceRef `json:"evidence"`
	Reason       string      `json:"reason"`
}
type TaskVersion struct {
	TaskID       string `json:"task_id"`
	TaskRevision int64  `json:"task_revision"`
}
type PlanSnapshot struct {
	SchemaVersion  int64          `json:"schema_version"`
	PlanID         string         `json:"plan_id"`
	TravelThreadID string         `json:"travel_thread_id"`
	Revision       int64          `json:"revision"`
	Definition     PlanDefinition `json:"definition"`
	TaskVersions   []TaskVersion  `json:"task_versions"`
	Selections     []Selection    `json:"selections"`
}
type ItemView struct {
	TaskID                     string  `json:"task_id"`
	TaskRevision               int64   `json:"task_revision"`
	ItemID                     string  `json:"item_id"`
	NeedsQuery                 bool    `json:"needs_query"`
	Status                     string  `json:"status"`
	LastSuccessfulTaskRevision *int64  `json:"last_successful_task_revision"`
	Reason                     *string `json:"reason"`
}
type PlanView struct {
	Snapshot PlanSnapshot `json:"snapshot"`
	Items    []ItemView   `json:"items"`
}
type FieldChanges struct {
	Set   Field[map[string]json.RawMessage] `json:"set,omitzero"`
	Unset Field[[]string]                   `json:"unset,omitzero"`
}

// Value is a tagged union payload: decode it into Stop/Leg/TaskDefinition/
// Requirement/Selection according to Op only after validating the operation.
type PatchOperation struct {
	Op            string                 `json:"op"`
	Value         Field[json.RawMessage] `json:"value,omitzero"`
	Changes       Field[FieldChanges]    `json:"changes,omitzero"`
	StopID        Field[string]          `json:"stop_id,omitzero"`
	LegID         Field[string]          `json:"leg_id,omitzero"`
	TaskID        Field[string]          `json:"task_id,omitzero"`
	RequirementID Field[string]          `json:"requirement_id,omitzero"`
	SelectionID   Field[string]          `json:"selection_id,omitzero"`
	ItemIDs       Field[[]string]        `json:"item_ids,omitzero"`
	StopIDs       Field[[]string]        `json:"stop_ids,omitzero"`
	TaskIDs       Field[[]string]        `json:"task_ids,omitzero"`
	Reason        Field[string]          `json:"reason,omitzero"`
}
type PlanPatch struct {
	SchemaVersion    int64            `json:"schema_version"`
	PlanID           string           `json:"plan_id"`
	ExpectedRevision int64            `json:"expected_revision"`
	Reason           string           `json:"reason"`
	Operations       []PatchOperation `json:"operations"`
}
type CommitResult struct {
	OperationID        string   `json:"operation_id"`
	PlanID             string   `json:"plan_id"`
	PreviousRevision   int64    `json:"previous_revision"`
	CommittedRevision  int64    `json:"committed_revision"`
	Changed            bool     `json:"changed"`
	InvalidatedTaskIDs []string `json:"invalidated_task_ids"`
	RefreshedItemIDs   []string `json:"refreshed_item_ids"`
}
type PlanDirectoryEntry struct {
	PlanID         string   `json:"plan_id"`
	TravelThreadID string   `json:"travel_thread_id"`
	Title          string   `json:"title"`
	Cities         []string `json:"cities"`
	StartDate      *string  `json:"start_date"`
	EndDate        *string  `json:"end_date"`
	RequestScope   string   `json:"request_scope"`
	Revision       int64    `json:"revision"`
	Lifecycle      string   `json:"lifecycle"`
	Progress       string   `json:"progress"`
	UpdatedAt      string   `json:"updated_at"`
}
type PlannerInput struct {
	SchemaVersion     int64          `json:"schema_version"`
	Request           string         `json:"request"`
	DelegationContext ContextSummary `json:"delegation_context"`
	WorkingMemory     WorkingMemory  `json:"working_memory"`
	PolicyStatus      string         `json:"policy_status"`
	Facts             []ContextFact  `json:"facts"`
}
type ReplanHistory struct {
	Reason            string   `json:"reason"`
	Changes           []string `json:"changes"`
	Outcome           string   `json:"outcome"`
	CommittedRevision int64    `json:"committed_revision"`
}
type ReplannerInput struct {
	SchemaVersion int64           `json:"schema_version"`
	Request       string          `json:"request"`
	Plan          PlanView        `json:"plan"`
	Execution     ExecutionReport `json:"execution"`
	PriorReplans  []ReplanHistory `json:"prior_replans"`
}
type ReplannerDecision struct {
	Kind        string           `json:"kind"`
	Patch       Field[PlanPatch] `json:"patch,omitzero"`
	TaskOutcome Field[string]    `json:"task_outcome,omitzero"`
	Summary     Field[string]    `json:"summary,omitzero"`
	Unresolved  Field[[]string]  `json:"unresolved,omitzero"`
}
