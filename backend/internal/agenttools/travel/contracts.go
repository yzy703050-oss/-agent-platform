package travel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"agent-platform/backend/internal/model"
)

// Native dictionaries retain their leaf values until schema validation and
// deterministic business conversion. Raw messages preserve null, decimal and
// explicit clearing; no dictionary is passed unchecked into Service.
type NativeConditions map[string]json.RawMessage

type NativeTripArguments struct {
	RequestKind  string       `json:"request_kind"`
	Tripplan     NativePlan   `json:"tripplan"`
	Reply        *NativeReply `json:"reply,omitempty"`
	RoundSummary *string      `json:"round_summary,omitempty"`
	// Accepted only for source API compatibility; not exposed to the model.
	FinalAnswer    *string               `json:"final_answer,omitempty"`
	GoalAssessment *NativeGoalAssessment `json:"goal_assessment,omitempty"`
}

type NativeGoalAssessment struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}
type NativeReply struct {
	Tasks       map[string][]string `json:"tasks"`
	Explanation string              `json:"explanation"`
}

type NativePlan struct {
	Operation           string            `json:"operation"`
	TripPlanID          *string           `json:"trip_plan_id,omitempty"`
	ExpectedRevision    *int64            `json:"expected_revision,omitempty"`
	ConfirmedConditions NativeConditions  `json:"confirmed_conditions,omitempty"`
	FieldSources        map[string]string `json:"field_sources,omitempty"`
	ConditionStrengths  map[string]string `json:"condition_strengths,omitempty"`
	Tasks               []NativeTask      `json:"tasks,omitempty"`
	RouteTasks          *[]NativeTask     `json:"route_tasks,omitempty"`
	RepairIssueCodes    []string          `json:"repair_issue_codes,omitempty"`
	DefaultRoundTrip    *bool             `json:"default_round_trip,omitempty"`
	AllowRepair         *bool             `json:"allow_repair,omitempty"`
	Reason              *string           `json:"reason,omitempty"`
}

type NativeTask struct {
	TaskID             *string                             `json:"task_id,omitempty"`
	ExpectedRevision   *int64                              `json:"expected_revision,omitempty"`
	Origin             *string                             `json:"origin,omitempty"`
	Destination        *string                             `json:"destination,omitempty"`
	Purpose            *string                             `json:"purpose,omitempty"`
	RequiresHotel      *bool                               `json:"requires_hotel,omitempty"`
	Conditions         NativeConditions                    `json:"conditions,omitempty"`
	FieldSources       map[string]string                   `json:"field_sources,omitempty"`
	ConditionStrengths map[string]string                   `json:"condition_strengths,omitempty"`
	Items              *[]NativeQueryItem                  `json:"items,omitempty"`
	Content            *[]model.TripActivity               `json:"content,omitempty"`
	SelectionRefs      map[string]model.TravelCandidateRef `json:"selection_refs,omitempty"`
	Components         []string                            `json:"components,omitempty"`
	present            map[string]json.RawMessage
}

func (t *NativeTask) UnmarshalJSON(data []byte) error {
	type plain NativeTask
	var value plain
	if err := strictDecode(data, &value); err != nil {
		return err
	}
	*t = NativeTask(value)
	return json.Unmarshal(data, &t.present)
}
func (t NativeTask) Has(field string) bool { _, ok := t.present[field]; return ok }

type NativeRanking struct {
	Field     string          `json:"field"`
	Direction string          `json:"direction,omitempty"`
	Preferred json.RawMessage `json:"preferred,omitempty"`
}
type NativeQueryItem struct {
	Domain             string            `json:"domain"`
	Parameters         NativeConditions  `json:"parameters,omitempty"`
	Constraints        NativeConditions  `json:"constraints,omitempty"`
	FieldSources       map[string]string `json:"field_sources,omitempty"`
	ConditionStrengths map[string]string `json:"condition_strengths,omitempty"`
	Ranking            []NativeRanking   `json:"ranking,omitempty"`
	DependsOn          []string          `json:"depends_on,omitempty"`
	Refresh            bool              `json:"refresh,omitempty"`
}
type NativeQueryArguments struct {
	RequestKind string            `json:"request_kind"`
	QueryPlan   []NativeQueryItem `json:"query_plan"`
}
type NativeReadArguments struct {
	RequestKind string           `json:"request_kind"`
	Resource    string           `json:"resource"`
	SessionID   *string          `json:"session_id,omitempty"`
	TripPlanID  *string          `json:"trip_plan_id,omitempty"`
	Offset      int              `json:"offset,omitempty"`
	Filters     NativeConditions `json:"filters,omitempty"`
}
type NativeMemoryUpdate struct {
	Op   string  `json:"op"`
	Text *string `json:"text,omitempty"`
	ID   *string `json:"id,omitempty"`
}
type NativeMemoryArguments struct {
	RequestKind string               `json:"request_kind"`
	Operation   string               `json:"operation,omitempty"`
	Updates     []NativeMemoryUpdate `json:"updates,omitempty"`
}
type NativePolicyArguments struct {
	RequestKind string           `json:"request_kind"`
	Context     NativeConditions `json:"context,omitempty"`
	TripPlanID  *string          `json:"trip_plan_id,omitempty"`
}
type ReplanArguments struct {
	RequestKind string   `json:"request_kind"`
	TripPlanIDs []string `json:"trip_plan_ids"`
	Reason      string   `json:"reason"`
}

func strictDecode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values in tool arguments")
		}
		return fmt.Errorf("unexpected data after tool arguments: %w", err)
	}
	return nil
}
