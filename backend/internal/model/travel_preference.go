package model

type TravelPreferenceCommand struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	// Set by the caller: model tool arguments cannot label inference as explicit.
	Origin string `json:"-"`
}
type TravelPreference struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Revision    int64  `json:"revision"`
	Retracted   bool   `json:"retracted"`
	Origin      string `json:"origin"`
	SourceRunID string `json:"source_run_id"`
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
