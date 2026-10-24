package travel

import (
	"context"
	"errors"

	"agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"golang.org/x/sync/errgroup"
)

// QueryJob is generated from the committed mutation, not an invented LLM call.
// Its parent identity is stable across checkpoint reconstruction and attempts.
type QueryJob = model.TravelQueryJob

func PlanQueryJobs(operationID, callID string, result model.TripResult) ([]QueryJob, error) {
	if operationID == "" || callID == "" || result.Receipt == nil || result.Receipt.OperationID != operationID || result.Receipt.PlanID != result.Plan.ID || result.Receipt.Revision != result.Plan.Revision {
		return nil, model.ErrIdempotencyConflict
	}
	jobs := make([]QueryJob, 0, len(result.Queries))
	for i, q := range result.Queries {
		if q.PlanID != result.Plan.ID || q.PlanRevision != result.Plan.Revision || q.TaskID == "" {
			return nil, model.ErrIdempotencyConflict
		}
		jobs = append(jobs, QueryJob{ID: model.TravelQueryJobID(operationID, i, q), ParentCallID: callID, Query: q})
	}
	return jobs, nil
}

// ExecuteQueryJobs runs ready independent requests in bounded waves. A result
// callback persists each actual completion before any dependent wave starts.
// Task dependencies order observations; they never fabricate arrival facts.
func (r *Registry) ExecuteQueryJobs(ctx context.Context, jobs []QueryJob, record func(context.Context, QueryJob) error) ([]QueryJob, error) {
	out := append([]QueryJob(nil), jobs...)
	queries := make([]model.TravelQuery, 0, len(jobs))
	for _, job := range jobs {
		queries = append(queries, job.Query)
	}
	if err := tripsvc.ValidateQueryDependencies(queries); err != nil {
		return out, err
	}
	for {
		pending := false
		ready := []int{}
		for i, job := range out {
			if job.Result != nil {
				continue
			}
			pending = true
			blocked := false
			for _, dep := range job.Query.DependsOn {
				for j, other := range out {
					if j != i && other.Query.PlanID == job.Query.PlanID && other.Query.TaskID == dep && other.Result == nil {
						blocked = true
					}
				}
			}
			if !blocked {
				ready = append(ready, i)
			}
		}
		if !pending {
			return out, nil
		}
		if len(ready) == 0 {
			return out, ruleError("QUERY_DEPENDENCY_CYCLE", "query dependencies contain a cycle", nil)
		}
		group, qctx := errgroup.WithContext(ctx)
		group.SetLimit(3)
		for _, i := range ready {
			group.Go(func() error {
				var e model.TravelEvidence
				var err error
				bounded, cancel := context.WithTimeout(qctx, nativeTimeout(r.cfg.Timeout))
				defer cancel()
				if executor, ok := r.cfg.Trips.(interface {
					ExecuteQueryJob(context.Context, model.TravelExecution, string, model.TravelQuery) (model.TravelEvidence, error)
				}); ok {
					e, err = executor.ExecuteQueryJob(bounded, r.cfg.Execution, out[i].ID, out[i].Query)
				} else {
					e, err = r.cfg.Trips.Query(bounded, r.cfg.Execution, out[i].Query)
				}
				if err != nil {
					if errors.Is(err, model.ErrTravelOutcomeUnknown) || errors.Is(err, model.ErrConflict) || errors.Is(err, model.ErrIdempotencyConflict) {
						return err
					}
					if qctx.Err() != nil {
						return qctx.Err()
					}
					e = model.TravelEvidence{Status: "error", Query: out[i].Query, Message: "查询失败，供应商未返回可核实结果。"}
				}
				job := out[i]
				job.Result = &e
				if record != nil {
					if err := record(qctx, job); err != nil {
						return err
					}
				}
				out[i] = job
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return out, err
		}
	}
}
