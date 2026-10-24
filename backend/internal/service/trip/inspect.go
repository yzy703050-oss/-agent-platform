package trip

import (
	"context"
	"errors"
	"slices"

	"agent-platform/backend/internal/model"
)

// Inspect preserves the historical revision but never presents an expired
// selection as current evidence. No business revision is written by inspection.
func (s *Service) Inspect(ctx context.Context, ref model.TravelExecution, id string) (model.TripResult, error) {
	p, err := s.readForExecution(ctx, ref, id)
	if err != nil {
		return model.TripResult{}, err
	}
	if p.ConversationID != ref.ConversationID {
		return model.TripResult{}, model.ErrNotFound
	}
	var expired []model.TripIssue
	for i := range p.Tasks {
		for _, slot := range []struct {
			domain    string
			selection **model.TravelSelection
		}{{"train", &p.Tasks[i].Train}, {"hotel", &p.Tasks[i].Hotel}} {
			choice := *slot.selection
			if choice == nil {
				continue
			}
			evidence, err := s.store.LoadTravelEvidence(ctx, ref, []model.TravelCandidateRef{choice.Ref})
			if err != nil && !errors.Is(err, model.ErrNotFound) {
				return model.TripResult{}, err
			}
			if errors.Is(err, model.ErrNotFound) || len(evidence) != 1 || !evidence[0].ExpiresAt.After(s.now()) {
				*slot.selection = nil
				expired = append(expired, model.TripIssue{Code: "selection_expired", TaskID: p.Tasks[i].ID, Field: slot.domain, Message: "已保存的候选依据已过期，请重新查询并选择。", Blocking: true})
			}
		}
	}
	v, queries, err := validatePlan(p)
	if err != nil {
		return model.TripResult{}, err
	}
	if p.SourceVersion > 0 {
		queries, err = sourceQueryRequests(p, model.TripCommand{Operation: "propose"})
		if err != nil {
			return model.TripResult{}, err
		}
		queries = slices.DeleteFunc(queries, func(q model.TravelQuery) bool {
			for _, t := range p.Tasks {
				if t.ID == q.TaskID && (q.Domain == "train" && t.Train != nil || q.Domain == "hotel" && t.Hotel != nil) {
					return true
				}
			}
			for _, e := range p.QueryResults {
				if e.TaskID == q.TaskID && e.Query.Domain == q.Domain && e.Query.TaskRevision == q.TaskRevision && e.ExpiresAt.After(s.now()) {
					return true
				}
			}
			return false
		})
	}
	v.Issues = append(v.Issues, expired...)
	if len(expired) > 0 && v.Status == "complete" {
		v.Status = "partial"
	}
	p.Validation = v
	return model.TripResult{Plan: p, Validation: v, Queries: queries}, nil
}
