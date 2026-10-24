package trip

import (
	"context"
	"encoding/json"
	"slices"

	"agent-platform/backend/internal/model"
)

type SourceHistoryStore interface {
	ReadTravelHistory(context.Context, model.TravelExecution, model.TravelHistoryRequest) (json.RawMessage, error)
	ReadTravelAnalysisTrips(context.Context, model.TravelExecution) ([]model.TripPlan, error)
}

func (s *Service) ReadHistory(ctx context.Context, ref model.TravelExecution, request model.TravelHistoryRequest) (json.RawMessage, error) {
	if !slices.Contains([]string{"trip_summaries", "trip", "session_summaries", "session"}, request.Resource) || request.Offset < 0 {
		return nil, invalid("unsupported read resource or offset")
	}
	if request.Resource == "trip" && request.TripPlanID == "" {
		return nil, invalid("trip_not_found")
	}
	if request.Resource == "session" && request.ConversationID == "" {
		return nil, invalid("session_not_found")
	}
	if request.Resource == "session_summaries" {
		for key := range request.Filters {
			if key != "query" {
				return nil, invalid("unsupported session filters")
			}
		}
	}
	store, ok := s.store.(SourceHistoryStore)
	if !ok {
		return nil, model.ErrAgentUnavailable
	}
	return store.ReadTravelHistory(ctx, ref, request)
}
func (s *Service) ReadAnalysisHistory(ctx context.Context, ref model.TravelExecution) ([]model.TripPlan, error) {
	store, ok := s.store.(SourceHistoryStore)
	if !ok {
		return nil, model.ErrAgentUnavailable
	}
	return store.ReadTravelAnalysisTrips(ctx, ref)
}
