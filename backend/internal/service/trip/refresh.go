package trip

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"agent-platform/backend/internal/model"
)

func (s *Service) PrepareRefresh(ctx context.Context, ref model.TravelExecution, id string) (model.TripRefreshPlan, error) {
	p, err := s.Read(ctx, ref.Scope, id)
	if err != nil {
		return model.TripRefreshPlan{}, err
	}
	if p.ConversationID != ref.ConversationID {
		return model.TripRefreshPlan{}, model.ErrNotFound
	}
	return PrepareSourceRefresh(p, s.now())
}
func PrepareSourceRefresh(p model.TripPlan, now time.Time) (model.TripRefreshPlan, error) {
	out := model.TripRefreshPlan{PlanID: p.ID, ExpectedRevision: p.Revision}
	if p.StopReason == "paused" || p.StopReason == "cancelled" {
		return out, nil
	}
	domains := map[string][]string{}
	for _, e := range p.QueryResults {
		if e.ExpiresAt.IsZero() || e.ExpiresAt.After(now) {
			continue
		}
		task := slices.IndexFunc(p.Tasks, func(t model.TripTask) bool { return t.ID == e.TaskID && max(1, t.Revision) == e.Query.TaskRevision })
		if task < 0 {
			continue
		}
		out.Invalidated = append(out.Invalidated, model.TravelCandidateRef{QueryID: e.ID, ResultRevision: e.ResultRevision})
		domains[e.TaskID] = append(domains[e.TaskID], e.Query.Domain)
	}
	cmd := model.TripCommand{Operation: "amend"}
	for _, task := range p.Tasks {
		if len(domains[task.ID]) == 0 {
			continue
		}
		items := []model.TripQueryItem{}
		for _, item := range task.Items {
			if slices.Contains(domains[task.ID], item.Domain) {
				items = append(items, item)
			}
		}
		cmd.Changes = append(cmd.Changes, model.TripTaskChange{TaskID: task.ID, Items: &items})
	}
	var err error
	out.Queries, err = sourceQueryRequests(p, cmd)
	if err != nil {
		return out, err
	}
	for i := range out.Queries {
		out.Queries[i].Refresh = true
	}
	slices.SortFunc(out.Invalidated, func(a, b model.TravelCandidateRef) int {
		if a.QueryID < b.QueryID {
			return -1
		}
		if a.QueryID > b.QueryID {
			return 1
		}
		return 0
	})
	return out, nil
}

func expiredSelection(previous *model.TripPlan, selections []model.TripSelection, now time.Time) error {
	if previous == nil || previous.SourceVersion == 0 {
		return nil
	}
	for _, s := range selections {
		for _, e := range previous.QueryResults {
			if e.ID == s.Ref.QueryID && e.ResultRevision == s.Ref.ResultRevision && !e.ExpiresAt.After(now) && slices.ContainsFunc(CandidateView(e).Candidates, func(c model.TravelCandidate) bool { return c.ID == s.Ref.CandidateID }) {
				return evidenceExpired(*previous, s.TaskID, e)
			}
		}
	}
	return nil
}
func evidenceExpired(p model.TripPlan, taskID string, e model.TravelEvidence) error {
	details := map[string]json.RawMessage{"trip_plan_id": rawValue(p.ID), "task_id": rawValue(taskID), "query_id": rawValue(e.ID), "result_revision": rawValue(e.ResultRevision)}
	return &model.TravelRuleError{Code: "EVIDENCE_EXPIRED", Message: "candidate evidence expired; refresh before selecting", Details: details}
}
