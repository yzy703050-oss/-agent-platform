package model

type TravelMemoryUpdate struct {
	Op     string  `json:"op"`
	Text   *string `json:"text,omitempty"`
	ID     *string `json:"id,omitempty"`
	Source string  `json:"-"`
}
type TravelMemoryCommand struct {
	Operation string               `json:"operation"`
	Updates   []TravelMemoryUpdate `json:"updates"`
}
type TravelMemoryApplied struct {
	Op string `json:"op"`
	ID string `json:"id"`
}
type TravelMemoryResult struct {
	Status   string                `json:"status,omitempty"`
	Reason   string                `json:"reason,omitempty"`
	Applied  []TravelMemoryApplied `json:"applied"`
	Rejected []TravelMemoryUpdate  `json:"rejected"`
}

type TravelMemoryCommit struct {
	SourceRuns               []string
	Execution                TravelExecution
	OperationID, RequestHash string
	ExpectedVersions         map[string]int64
	Changes                  []TravelPreferenceCommand
	Result                   TravelMemoryResult
}

type TravelPreferenceAnalysis struct {
	InitialSummaryDone bool     `json:"initial_summary_done"`
	CoveredTripIDs     []string `json:"covered_trip_ids"`
	MemoryIDs          []string `json:"memory_ids"`
	Summary            string   `json:"summary"`
	Trigger            string   `json:"trigger"`
}

type TravelPreferenceCommand struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	// Set by the caller: model tool arguments cannot label inference as explicit.
	Origin string `json:"-"`
}
type TravelPreference struct {
	SourceRuns  []string `json:"source_runs,omitempty"`
	Key         string   `json:"key"`
	Value       string   `json:"value"`
	Revision    int64    `json:"revision"`
	Retracted   bool     `json:"retracted"`
	Origin      string   `json:"origin"`
	SourceRunID string   `json:"source_run_id"`
}
type TravelPreferenceCommit struct {
	Execution   TravelExecution
	OperationID string
	RequestHash string
	Command     TravelPreferenceCommand
}
type TravelSummary struct {
	ID          string   `json:"id"`
	SourceRunID string   `json:"source_run_id"`
	SourceRuns  []string `json:"source_runs"`
	Version     int64    `json:"version"`
	Content     string   `json:"content"`
}
