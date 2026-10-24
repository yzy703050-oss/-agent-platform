package trip

import (
	"context"

	"agent-platform/backend/internal/model"
)

type QueryJobStore interface {
	BeginTravelQueryJob(context.Context, model.TravelExecution, string, model.TravelQuery) (model.TravelEvidence, bool, error)
	FinishTravelQueryJob(context.Context, model.TravelExecution, string, model.TravelEvidence) (model.TravelEvidence, error)
	ListTravelQueryJobs(context.Context, model.TravelExecution) ([]model.TravelQueryJob, error)
	EnqueueTravelRefresh(context.Context, model.TravelExecution, string, model.TripRefreshPlan) ([]model.TravelQueryJob, error)
}

func (s *Service) ExecuteQueryJob(ctx context.Context, ref model.TravelExecution, id string, q model.TravelQuery) (model.TravelEvidence, error) {
	store, ok := s.store.(QueryJobStore)
	if !ok {
		return model.TravelEvidence{}, model.ErrAgentUnavailable
	}
	result, done, err := store.BeginTravelQueryJob(ctx, ref, id, q)
	if err != nil || done {
		return result, err
	}
	ref.QueryJobID = id
	result, err = s.Query(ctx, ref, q)
	if err != nil {
		return result, err
	}
	if result.Query.Domain == "" {
		result.Query = q
	}
	return store.FinishTravelQueryJob(ctx, ref, id, result)
}
func (s *Service) ListQueryJobs(ctx context.Context, ref model.TravelExecution) ([]model.TravelQueryJob, error) {
	store, ok := s.store.(QueryJobStore)
	if !ok {
		return nil, model.ErrAgentUnavailable
	}
	return store.ListTravelQueryJobs(ctx, ref)
}
func (s *Service) EnqueueRefresh(ctx context.Context, ref model.TravelExecution, operation string, plan model.TripRefreshPlan) ([]model.TravelQueryJob, error) {
	store, ok := s.store.(QueryJobStore)
	if !ok {
		return nil, model.ErrAgentUnavailable
	}
	return store.EnqueueTravelRefresh(ctx, ref, operation, plan)
}
