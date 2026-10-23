package model

import "time"

type ConstraintStrength string

const (
	Hard ConstraintStrength = "hard"
	Soft ConstraintStrength = "soft"
)

type TravelExecution struct {
	Scope          Scope
	ConversationID string
	RunID          string
	Attempt        int64
}

type TripConstraints struct {
	MaxBudgetFen   *int64                        `json:"max_budget_fen,omitempty"`
	BudgetStrength ConstraintStrength            `json:"budget_strength,omitempty"`
	TrainKinds     []string                      `json:"train_kinds,omitempty"`
	SeatClasses    []string                      `json:"seat_classes,omitempty"`
	Strengths      map[string]ConstraintStrength `json:"strengths,omitempty"`
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
	ID            string            `json:"id"`
	Origin        string            `json:"origin"`
	Destination   string            `json:"destination"`
	DepartureDate string            `json:"departure_date"`
	ArrivalDate   string            `json:"arrival_date,omitempty"`
	ArrivalBefore string            `json:"arrival_before,omitempty"`
	RequiresHotel bool              `json:"requires_hotel"`
	CheckIn       string            `json:"check_in,omitempty"`
	CheckOut      string            `json:"check_out,omitempty"`
	Nights        int               `json:"nights,omitempty"`
	Activities    []TripActivity    `json:"activities,omitempty"`
	Constraints   TripConstraints   `json:"constraints,omitempty"`
	FieldSources  map[string]string `json:"field_sources,omitempty"`
	Train         *TravelSelection  `json:"train,omitempty"`
	Hotel         *TravelSelection  `json:"hotel,omitempty"`
}

type TripIssue struct {
	Code     string `json:"code"`
	TaskID   string `json:"task_id,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}
type TripValidation struct {
	Status string      `json:"status"`
	Issues []TripIssue `json:"issues"`
}

type TripPlan struct {
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	ConversationID string          `json:"conversation_id"`
	SourceRunID    string          `json:"source_run_id"`
	Revision       int64           `json:"revision"`
	RoundTrip      bool            `json:"round_trip"`
	Tasks          []TripTask      `json:"tasks"`
	Constraints    TripConstraints `json:"constraints"`
	Validation     TripValidation  `json:"validation"`
}

// Patch pointers distinguish omitted fields from an explicit clear or zero.
type TripTaskChange struct {
	TaskID        string            `json:"task_id"`
	Origin        *string           `json:"origin,omitempty"`
	Destination   *string           `json:"destination,omitempty"`
	DepartureDate *string           `json:"departure_date,omitempty"`
	ArrivalDate   *string           `json:"arrival_date,omitempty"`
	ArrivalBefore *string           `json:"arrival_before,omitempty"`
	RequiresHotel *bool             `json:"requires_hotel,omitempty"`
	CheckIn       *string           `json:"check_in,omitempty"`
	CheckOut      *string           `json:"check_out,omitempty"`
	Nights        *int              `json:"nights,omitempty"`
	Activities    *[]TripActivity   `json:"activities,omitempty"`
	Constraints   *TripConstraints  `json:"constraints,omitempty"`
	FieldSources  map[string]string `json:"field_sources,omitempty"`
}

type TripSelection struct {
	TaskID string             `json:"task_id"`
	Domain string             `json:"domain"`
	Ref    TravelCandidateRef `json:"ref"`
}

type TripCommand struct {
	Operation        string           `json:"operation"`
	PlanID           string           `json:"plan_id,omitempty"`
	ExpectedRevision int64            `json:"expected_revision,omitempty"`
	RoundTrip        *bool            `json:"round_trip,omitempty"`
	Tasks            []TripTask       `json:"tasks,omitempty"`
	Changes          []TripTaskChange `json:"changes,omitempty"`
	Selections       []TripSelection  `json:"selections,omitempty"`
	Constraints      *TripConstraints `json:"constraints,omitempty"`
}

type TravelReceipt struct {
	OperationID string `json:"operation_id"`
	PlanID      string `json:"plan_id,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
	RequestHash string `json:"-"`
}
type TripResult struct {
	Plan       TripPlan       `json:"plan"`
	Validation TripValidation `json:"validation"`
	Queries    []TravelQuery  `json:"queries"`
	Receipt    *TravelReceipt `json:"receipt,omitempty"`
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
