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
		// Saved maps retain old domains across a route change. Inspection only
		// expands queries still in the current task scope; it must not reject a
		// valid transit task because its prior destination needed a hotel.
		command := model.TripCommand{Operation: "amend"}
		for _, task := range p.Tasks {
			items := []model.TripQueryItem{}
			for _, item := range task.Items {
				if item.Domain != "hotel" || task.RequiresHotel {
					items = append(items, item)
				}
			}
			command.Changes = append(command.Changes, model.TripTaskChange{TaskID: task.ID, Items: &items})
		}
		queries, err = sourceQueryRequests(p, command)
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
