package trip

import (
	"agent-platform/backend/internal/model"
	"slices"
	"time"
)

func selectCandidate(p model.TripPlan, task model.TripTask, selection model.TripSelection, evidence []model.TravelEvidence, now time.Time) (model.TravelSelection, error) {
	if selection.Domain != "train" && selection.Domain != "hotel" {
		return model.TravelSelection{}, invalid("invalid selection domain")
	}
	for _, e := range evidence {
		if e.ID != selection.Ref.QueryID || e.ResultRevision != selection.Ref.ResultRevision {
			continue
		}
		if e.UserID != p.UserID || e.ConversationID != p.ConversationID || e.PlanID != p.ID || e.TaskID != task.ID || e.Query.Domain != selection.Domain {
			return model.TravelSelection{}, invalid("candidate scope or binding mismatch")
		}
		if p.SourceVersion > 0 && e.Query.TaskRevision != max(task.Revision, 1) {
			return model.TravelSelection{}, invalid("QUERY_REBIND_REQUIRED: candidate belongs to an earlier task revision")
		}
		if (e.Status != "ok" && e.Status != "partial") || e.Source.Provider == "" || e.Source.FetchedAt.IsZero() || e.Source.FetchedAt.After(now) || !e.ExpiresAt.After(now) {
			if p.SourceVersion > 0 && !e.ExpiresAt.After(now) && slices.ContainsFunc(CandidateView(e).Candidates, func(c model.TravelCandidate) bool { return c.ID == selection.Ref.CandidateID }) {
				return model.TravelSelection{}, evidenceExpired(p, task.ID, e)
			}
			return model.TravelSelection{}, invalid("candidate evidence is missing or expired")
		}
		if p.SourceVersion > 0 {
			if e.Query.TaskRevision != max(task.Revision, 1) {
				return model.TravelSelection{}, invalid("QUERY_REBIND_REQUIRED: candidate belongs to an earlier task revision")
			}
			view := CandidateView(e)
			visible := false
			for _, c := range view.Candidates {
				if c.ID == selection.Ref.CandidateID {
					visible = true
					break
				}
			}
			if !visible {
				return model.TravelSelection{}, invalid("candidate was not in the current bounded tool view")
			}
		}
		if selection.Domain == "train" && (e.Query.Origin != task.Origin || e.Query.Destination != task.Destination || e.Query.Date != task.DepartureDate) {
			return model.TravelSelection{}, invalid("candidate query no longer matches route")
		}
		if selection.Domain == "hotel" && (!task.RequiresHotel || e.Query.City != task.Destination || e.Query.CheckIn != task.CheckIn || e.Query.CheckOut != task.CheckOut) {
			return model.TravelSelection{}, invalid("candidate query no longer matches stay")
		}
		for _, c := range e.Candidates {
			if c.ID != selection.Ref.CandidateID {
				continue
			}
			if c.Name == "" || (c.PriceFen != nil && *c.PriceFen < 0) {
				return model.TravelSelection{}, invalid("invalid candidate facts")
			}
			if selection.Domain == "train" {
				if c.Origin != task.Origin || c.Destination != task.Destination {
					return model.TravelSelection{}, invalid("candidate route mismatch")
				}
				if c.DepartureAt != nil && c.DepartureAt.In(beijing).Format("2006-01-02") != task.DepartureDate {
					return model.TravelSelection{}, invalid("candidate departure date mismatch")
				}
				for _, constraints := range []model.TripConstraints{p.Constraints, task.Constraints} {
					if !matchesTrain(c, constraints) {
						return model.TravelSelection{}, invalid("candidate violates train constraints")
					}
				}
				if task.ArrivalBefore != "" {
					deadline, err := time.Parse(time.RFC3339, task.ArrivalBefore)
					if err != nil || c.ArrivalAt == nil || c.ArrivalAt.After(deadline) {
						return model.TravelSelection{}, invalid("candidate cannot verify arrival deadline")
					}
				}
			}
			if selection.Domain == "hotel" && c.City != task.Destination {
				return model.TravelSelection{}, invalid("hotel city mismatch")
			}
			copied, err := clone(c)
			if err != nil {
				return model.TravelSelection{}, err
			}
			return model.TravelSelection{Ref: selection.Ref, Candidate: copied}, nil
		}
		return model.TravelSelection{}, invalid("candidate does not exist in evidence")
	}
	return model.TravelSelection{}, invalid("candidate evidence not found")
}
