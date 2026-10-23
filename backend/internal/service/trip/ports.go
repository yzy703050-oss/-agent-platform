package trip

import (
	"agent-platform/backend/internal/model"
	"context"
)

type Store interface {
	GetTrip(context.Context, model.Scope, string) (model.TripPlan, error)
	CommitTrip(context.Context, model.TripCommit) (model.TripResult, error)
	LoadTravelEvidence(context.Context, model.TravelExecution, []model.TravelCandidateRef) ([]model.TravelEvidence, error)
	FindTripReceipt(context.Context, model.TravelExecution, string, string) (model.TripResult, bool, error)
}

type QueryClient interface {
	Search(context.Context, model.TravelQuery) (model.TravelEvidence, error)
}
