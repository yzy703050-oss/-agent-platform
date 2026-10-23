package model

import "time"

type TravelQuery struct {
	Domain       string          `json:"domain"`
	PlanID       string          `json:"plan_id,omitempty"`
	PlanRevision int64           `json:"plan_revision,omitempty"`
	TaskID       string          `json:"task_id,omitempty"`
	Origin       string          `json:"origin,omitempty"`
	Destination  string          `json:"destination,omitempty"`
	Date         string          `json:"date,omitempty"`
	City         string          `json:"city,omitempty"`
	CheckIn      string          `json:"check_in,omitempty"`
	CheckOut     string          `json:"check_out,omitempty"`
	Keywords     string          `json:"keywords,omitempty"`
	Constraints  TripConstraints `json:"constraints,omitempty"`
}
type TravelSource struct {
	Provider  string    `json:"provider"`
	URL       string    `json:"url,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}
type TravelCandidate struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Origin      string     `json:"origin,omitempty"`
	Destination string     `json:"destination,omitempty"`
	DepartureAt *time.Time `json:"departure_at,omitempty"`
	ArrivalAt   *time.Time `json:"arrival_at,omitempty"`
	PriceFen    *int64     `json:"price_fen"`
	TrainKind   string     `json:"train_kind,omitempty"`
	SeatClass   string     `json:"seat_class,omitempty"`
	City        string     `json:"city,omitempty"`
	Address     string     `json:"address,omitempty"`
	URL         string     `json:"url,omitempty"`
}
type TravelCandidateRef struct {
	QueryID        string `json:"query_id"`
	ResultRevision int64  `json:"result_revision"`
	CandidateID    string `json:"candidate_id"`
}
type TravelEvidence struct {
	ID             string            `json:"id"`
	UserID         string            `json:"user_id"`
	ConversationID string            `json:"conversation_id"`
	SourceRunID    string            `json:"source_run_id"`
	PlanID         string            `json:"plan_id,omitempty"`
	TaskID         string            `json:"task_id,omitempty"`
	ResultRevision int64             `json:"result_revision"`
	Status         string            `json:"status"`
	Query          TravelQuery       `json:"query"`
	Source         TravelSource      `json:"source"`
	ExpiresAt      time.Time         `json:"expires_at"`
	Candidates     []TravelCandidate `json:"candidates"`
	MissingFields  []string          `json:"missing_fields,omitempty"`
	Message        string            `json:"message,omitempty"`
}
