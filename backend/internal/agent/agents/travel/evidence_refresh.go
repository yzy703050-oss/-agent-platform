package travel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
)

type sourceRefresher interface {
	PrepareRefresh(context.Context, pm.TravelExecution, string) (pm.TripRefreshPlan, error)
}

func (f *sourceFlow) enqueueRefresh(ctx context.Context, s *FlowState, id string) (bool, error) {
	if !s.Planning || s.PlanningRounds >= f.cfg.PlanningRounds || s.RefreshedPlans[id] || s.StopReason != "" {
		return false, nil
	}
	refresher, ok := f.cfg.Trips.(sourceRefresher)
	if !ok {
		return false, nil
	}
	plan, err := refresher.PrepareRefresh(ctx, f.cfg.Execution, id)
	if err != nil {
		return false, err
	}
	if len(plan.Invalidated) == 0 || len(plan.Queries) == 0 {
		return false, nil
	}
	keys := slices.Clone(s.ExternalQueryKeys)
	for _, q := range plan.Queries {
		keys = append(keys, tripsvc.ExternalQueryKeys(q)...)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if len(keys) > 12 {
		return false, nil
	}
	if s.RefreshedPlans == nil {
		s.RefreshedPlans = map[string]bool{}
	}
	s.RefreshedPlans[id] = true
	s.ExternalQueryKeys = keys
	for _, ref := range plan.Invalidated {
		if !slices.Contains(s.BlockedEvidence, ref) {
			s.BlockedEvidence = append(s.BlockedEvidence, ref)
		}
	}
	for i, q := range plan.Queries {
		raw, _ := json.Marshal([]string{f.cfg.Execution.RunID, "refresh", id, fmt.Sprint(i)})
		sum := sha256.Sum256(raw)
		s.Jobs = append(s.Jobs, traveltools.QueryJob{ID: hex.EncodeToString(sum[:]), ParentCallID: "refresh:" + id, Query: q})
	}
	return true, nil
}
func (f *sourceFlow) refreshBeforeReview(ctx context.Context, s *FlowState) (bool, error) {
	queued := false
	for _, id := range s.planIDs() {
		added, err := f.enqueueRefresh(ctx, s, id)
		if err != nil {
			return false, err
		}
		queued = queued || added
	}
	return queued, nil
}
func failedSourceRefresh(s FlowState) bool {
	if len(s.Plans) != 1 {
		return false
	}
	id := s.planIDs()[0]
	if !s.RefreshedPlans[id] {
		return false
	}
	hasFailure, hasItems := false, false
	for _, job := range s.queryViews(s.Plans[id].Plan) {
		if job.Result == nil {
			continue
		}
		if len(tripsvc.CandidateView(*job.Result).Candidates) > 0 {
			hasItems = true
		}
		if strings.HasPrefix(job.ParentCallID, "refresh:") && job.Result.Status != "ok" {
			hasFailure = true
		}
	}
	return hasFailure && !hasItems
}
