package model

type TravelHistoryRequest struct {
	Resource       string
	ConversationID string
	TripPlanID     string
	Offset         int
	Filters        TripConditions
}
type TravelPolicyQuery struct {
	Context    TripConditions
	TripPlanID string
}
