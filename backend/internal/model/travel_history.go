package model

import "time"

type TravelHistoryRequest struct {
	Resource       string
	ConversationID string
	TripPlanID     string
	Offset         int
	Filters        TripConditions
}
type TravelPolicyQuery struct {
	RetrievalTimeout time.Duration `json:"-"`
	Context          TripConditions
	TripPlanID       string
}
