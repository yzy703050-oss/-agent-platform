package model

import (
	"encoding/json"
	"time"
)

// TripConditions is the source contract's whitelisted leaf dictionary. Service
// validates every key and value; raw JSON preserves null and exact decimals.
type TripConditions map[string]json.RawMessage
type TripRanking struct {
	Field     string          `json:"field"`
	Direction string          `json:"direction,omitempty"`
	Preferred json.RawMessage `json:"preferred,omitempty"`
}
type TripQueryItem struct {
	Domain             string                        `json:"domain"`
	Parameters         TripConditions                `json:"parameters,omitempty"`
	Constraints        TripConditions                `json:"constraints,omitempty"`
	FieldSources       map[string]string             `json:"field_sources,omitempty"`
	ConditionStrengths map[string]ConstraintStrength `json:"condition_strengths,omitempty"`
	Ranking            []TripRanking                 `json:"ranking,omitempty"`
	DependsOn          []string                      `json:"depends_on,omitempty"`
	Refresh            bool                          `json:"refresh,omitempty"`
}

// Supplied by the authorized runtime, never by tool JSON.
type TripDecisionContext struct {
	PolicyScope   TripConditions
	EvidenceRefs  []TravelCandidateRef
	Role          string
	OriginalQuery string
	Policy        *SourcePolicyContext
	Preferences   map[string]string
}

type ConstraintStrength string

const (
	Hard ConstraintStrength = "hard"
	Soft ConstraintStrength = "soft"
)

type TravelExecution struct {
	QueryJobID     string
	Scope          Scope
	ConversationID string
	RunID          string
	Attempt        int64
}

type TripConstraints struct {
	MaxBudgetFen        *int64                        `json:"max_budget_fen,omitempty"`
	BudgetStrength      ConstraintStrength            `json:"budget_strength,omitempty"`
	TrainKinds          []string                      `json:"train_kinds,omitempty"`
	SeatClasses         []string                      `json:"seat_classes,omitempty"`
	Strengths           map[string]ConstraintStrength `json:"strengths,omitempty"`
	MaxTrainFen         *int64                        `json:"max_train_fen,omitempty"`
	MaxHotelNightFen    *int64                        `json:"max_hotel_night_fen,omitempty"`
	MaxHotelTotalFen    *int64                        `json:"max_hotel_total_fen,omitempty"`
	DepartureTimeAfter  string                        `json:"departure_time_after,omitempty"`
	DepartureTimeBefore string                        `json:"departure_time_before,omitempty"`
	AvailableOnly       *bool                         `json:"available_only,omitempty"`
}

type TripActivity struct {
	ID          string             `json:"id,omitempty"`
	Purpose     string             `json:"purpose"`
	Description string             `json:"description,omitempty"`
	Date        string             `json:"date,omitempty"`
	Time        string             `json:"time,omitempty"`
	Source      string             `json:"source"`
	Strength    ConstraintStrength `json:"strength"`
}

type TravelSelection struct {
	Ref       TravelCandidateRef `json:"ref"`
	Candidate TravelCandidate    `json:"candidate"`
}

type TripTask struct {
	ID                 string                        `json:"id"`
	Revision           int64                         `json:"revision,omitempty"`
	Purpose            string                        `json:"purpose,omitempty"`
	Conditions         TripConditions                `json:"conditions,omitempty"`
	ConditionStrengths map[string]ConstraintStrength `json:"condition_strengths,omitempty"`
	Items              []TripQueryItem               `json:"items,omitempty"`
	Passengers         int                           `json:"passengers,omitempty"`
	Guests             int                           `json:"guests,omitempty"`
	HotelKeywords      string                        `json:"hotel_keywords,omitempty"`
	HotelQuoteRequired bool                          `json:"hotel_quote_required,omitempty"`
	Origin             string                        `json:"origin"`
	Destination        string                        `json:"destination"`
	DepartureDate      string                        `json:"departure_date"`
	ArrivalDate        string                        `json:"arrival_date,omitempty"`
	ArrivalBefore      string                        `json:"arrival_before,omitempty"`
	RequiresHotel      bool                          `json:"requires_hotel"`
	CheckIn            string                        `json:"check_in,omitempty"`
	CheckOut           string                        `json:"check_out,omitempty"`
	Nights             int                           `json:"nights,omitempty"`
	Activities         []TripActivity                `json:"activities,omitempty"`
	Constraints        TripConstraints               `json:"constraints,omitempty"`
	FieldSources       map[string]string             `json:"field_sources,omitempty"`
	Train              *TravelSelection              `json:"train,omitempty"`
	Hotel              *TravelSelection              `json:"hotel,omitempty"`
}

type TripIssue struct {
	Code              string                `json:"code"`
	TaskID            string                `json:"task_id,omitempty"`
	Field             string                `json:"field,omitempty"`
	Message           string                `json:"message"`
	Blocking          bool                  `json:"blocking"`
	TaskIDs           []string              `json:"task_ids,omitempty"`
	Evidence          json.RawMessage       `json:"evidence,omitempty"`
	RepairPermissions map[string]bool       `json:"repair_permissions,omitempty"`
	Activity          *TripActivity         `json:"activity,omitempty"`
	ActivityScope     map[string]string     `json:"activity_scope,omitempty"`
	TravelWindow      map[string]*time.Time `json:"travel_window,omitempty"`
	CoarseWindow      map[string]string     `json:"coarse_window,omitempty"`
}
type TripValidation struct {
	Status string      `json:"status"`
	Issues []TripIssue `json:"issues"`
}

type TripPlan struct {
	QueryOrder          []string                      `json:"query_order,omitempty"`
	ID                  string                        `json:"id"`
	UserID              string                        `json:"user_id"`
	ConversationID      string                        `json:"conversation_id"`
	SourceRunID         string                        `json:"source_run_id"`
	Revision            int64                         `json:"revision"`
	RoundTrip           bool                          `json:"round_trip"`
	Tasks               []TripTask                    `json:"tasks"`
	Constraints         TripConstraints               `json:"constraints"`
	Validation          TripValidation                `json:"validation"`
	SourceVersion       int                           `json:"source_version,omitempty"`
	ConfirmedConditions TripConditions                `json:"confirmed_conditions,omitempty"`
	FieldSources        map[string]string             `json:"field_sources,omitempty"`
	ConditionStrengths  map[string]ConstraintStrength `json:"condition_strengths,omitempty"`
	PolicyContext       *SourcePolicyContext          `json:"policy_context,omitempty"`
	ConstraintSnapshot  json.RawMessage               `json:"constraint_snapshot,omitempty"`
	Status              string                        `json:"status,omitempty"`
	StopReason          string                        `json:"stop_reason,omitempty"`
	StopExplanation     string                        `json:"stop_explanation,omitempty"`
	UserConfirmed       bool                          `json:"user_confirmed,omitempty"`
	QueryResults        map[string]TravelEvidence     `json:"results_by_query,omitempty"`
}

// Patch pointers distinguish omitted fields from an explicit clear or zero.
type TripTaskChange struct {
	TaskID             string                        `json:"task_id"`
	Origin             *string                       `json:"origin,omitempty"`
	Destination        *string                       `json:"destination,omitempty"`
	DepartureDate      *string                       `json:"departure_date,omitempty"`
	ArrivalDate        *string                       `json:"arrival_date,omitempty"`
	ArrivalBefore      *string                       `json:"arrival_before,omitempty"`
	RequiresHotel      *bool                         `json:"requires_hotel,omitempty"`
	CheckIn            *string                       `json:"check_in,omitempty"`
	CheckOut           *string                       `json:"check_out,omitempty"`
	Nights             *int                          `json:"nights,omitempty"`
	Activities         *[]TripActivity               `json:"activities,omitempty"`
	Constraints        *TripConstraints              `json:"constraints,omitempty"`
	FieldSources       map[string]string             `json:"field_sources,omitempty"`
	ExpectedRevision   *int64                        `json:"expected_revision,omitempty"`
	Purpose            *string                       `json:"purpose,omitempty"`
	Conditions         TripConditions                `json:"conditions,omitempty"`
	ConditionStrengths map[string]ConstraintStrength `json:"condition_strengths,omitempty"`
	Items              *[]TripQueryItem              `json:"items,omitempty"`
	Components         []string                      `json:"components,omitempty"`
}

type TripSelection struct {
	ExpectedRevision *int64             `json:"expected_revision,omitempty"`
	TaskID           string             `json:"task_id"`
	Domain           string             `json:"domain"`
	Ref              TravelCandidateRef `json:"ref"`
}

type TripCommand struct {
	Operation           string                        `json:"operation"`
	PlanID              string                        `json:"plan_id,omitempty"`
	ExpectedRevision    int64                         `json:"expected_revision,omitempty"`
	RoundTrip           *bool                         `json:"round_trip,omitempty"`
	Tasks               []TripTask                    `json:"tasks,omitempty"`
	Changes             []TripTaskChange              `json:"changes,omitempty"`
	Selections          []TripSelection               `json:"selections,omitempty"`
	Constraints         *TripConstraints              `json:"constraints,omitempty"`
	ConfirmedConditions TripConditions                `json:"confirmed_conditions,omitempty"`
	FieldSources        map[string]string             `json:"field_sources,omitempty"`
	ConditionStrengths  map[string]ConstraintStrength `json:"condition_strengths,omitempty"`
	RouteTasks          *[]TripTaskChange             `json:"route_tasks,omitempty"`
	AllowRepair         *bool                         `json:"allow_repair,omitempty"`
	RepairIssueCodes    []string                      `json:"repair_issue_codes,omitempty"`
	Reason              string                        `json:"reason,omitempty"`
	Context             *TripDecisionContext          `json:"-"`
}

type TravelReceipt struct {
	OperationID string `json:"operation_id"`
	PlanID      string `json:"plan_id,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
	RequestHash string `json:"-"`
}
type TripResult struct {
	Operation    string         `json:"operation,omitempty"`
	RouteChanged bool           `json:"route_changed,omitempty"`
	Plan         TripPlan       `json:"plan"`
	Validation   TripValidation `json:"validation"`
	Queries      []TravelQuery  `json:"queries"`
	Receipt      *TravelReceipt `json:"receipt,omitempty"`
}

// TripReplySpec selects persisted components, never model-authored facts.
type TripReplySpec struct {
	Tasks       map[string][]string `json:"tasks,omitempty"`
	Explanation string              `json:"explanation,omitempty"`
}
type TripBoundReply struct {
	PlanID   string `json:"trip_plan_id"`
	Revision int64  `json:"revision"`
	Text     string `json:"rendered_answer"`
}

// TripCommit carries a service-validated snapshot. Repository rechecks identity,
// execution fencing, expected revision and immutable evidence guards in one transaction.
type TripCommit struct {
	Execution        TravelExecution
	OperationID      string
	RequestHash      string
	ExpectedRevision int64
	Result           TripResult
	Evidence         []TravelEvidence
	Now              time.Time
}
