package trip

import (
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

func basePlan() model.TripPlan {
	price := int64(18000)
	return model.TripPlan{ID: "plan", UserID: "owner", ConversationID: "conversation", Revision: 3, RoundTrip: true,
		Constraints: model.TripConstraints{MaxBudgetFen: &price, BudgetStrength: model.Hard},
		Tasks: []model.TripTask{
			{ID: "t1", Origin: "上海", Destination: "杭州", DepartureDate: "2026-10-25", Train: &model.TravelSelection{Ref: model.TravelCandidateRef{QueryID: "train-query", ResultRevision: 1, CandidateID: "G1"}, Candidate: model.TravelCandidate{ID: "G1", Name: "G1"}}},
			{ID: "t2", Origin: "杭州", Destination: "上海", DepartureDate: "2026-10-27"},
		}}
}

func TestTripRules(t *testing.T) {
	now := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)
	for _, name := range []string{"multileg", "missing_input", "invalid_date", "activity_only", "date_change", "hard_constraint", "candidate_binding", "expired_candidate", "unknown_price", "budget_exceeded", "task_budget_exceeded"} {
		t.Run(name, func(t *testing.T) {
			p := basePlan()
			cmd := model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision}
			var prior *model.TripPlan = &p
			var evidence []model.TravelEvidence
			switch name {
			case "multileg":
				prior = nil
				cmd = model.TripCommand{Operation: "propose", Tasks: p.Tasks}
				// Models may propose conditions, but not persisted selections or IDs.
				cmd.Tasks[0].Train = nil
			case "missing_input":
				prior = nil
				cmd = model.TripCommand{Operation: "propose", Tasks: []model.TripTask{{Destination: "杭州"}}}
			case "invalid_date":
				bad := "2026-02-30"
				cmd.Changes = []model.TripTaskChange{{TaskID: "t1", DepartureDate: &bad}}
			case "activity_only":
				activities := []model.TripActivity{{Purpose: "会议", Date: "2026-10-25", Source: "user", Strength: model.Hard}}
				cmd.Changes = []model.TripTaskChange{{TaskID: "t1", Activities: &activities}}
			case "date_change":
				date := "2026-10-26"
				cmd.Changes = []model.TripTaskChange{{TaskID: "t1", DepartureDate: &date}}
			case "hard_constraint":
				n := int64(999999)
				cmd.Constraints = &model.TripConstraints{MaxBudgetFen: &n, BudgetStrength: model.Soft}
			case "budget_exceeded":
				price := int64(30000)
				p.Tasks[0].Train.Candidate.PriceFen = &price
			case "task_budget_exceeded":
				price, budget := int64(11000), int64(10000)
				p.Tasks[0].Train.Candidate.PriceFen = &price
				p.Tasks[0].Constraints = model.TripConstraints{MaxBudgetFen: &budget, BudgetStrength: model.Hard}
			case "candidate_binding", "expired_candidate", "unknown_price":
				ref := model.TravelCandidateRef{QueryID: "q2", ResultRevision: 1, CandidateID: "G2"}
				cmd.Operation = "select"
				cmd.Selections = []model.TripSelection{{TaskID: "t2", Domain: "train", Ref: ref}}
				e := model.TravelEvidence{ID: "q2", UserID: "owner", ConversationID: "conversation", PlanID: "plan", TaskID: "t2", ResultRevision: 1, Status: "ok", Query: model.TravelQuery{Domain: "train", Origin: "杭州", Destination: "上海", Date: "2026-10-27"}, Source: model.TravelSource{Provider: "test", URL: "https://example.test/query", FetchedAt: now}, ExpiresAt: now.Add(time.Hour), Candidates: []model.TravelCandidate{{ID: "G2", Name: "G2", Origin: "杭州", Destination: "上海"}}}
				if name == "candidate_binding" {
					e.UserID = "another-user"
				}
				if name == "expired_candidate" {
					e.ExpiresAt = now.Add(-time.Second)
				}
				evidence = []model.TravelEvidence{e}
			}
			result, err := Preview(prior, cmd, evidence, now)
			if name == "invalid_date" || name == "hard_constraint" || name == "candidate_binding" || name == "expired_candidate" {
				if err == nil {
					t.Fatalf("unsafe change accepted: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "multileg":
				if len(result.Plan.Tasks) != 2 || result.Plan.Tasks[0].ID == "" {
					t.Fatalf("invalid plan: %+v", result)
				}
			case "missing_input":
				if result.Validation.Status != "needs_input" || len(result.Queries) != 0 {
					t.Fatalf("invented missing conditions: %+v", result)
				}
			case "activity_only":
				if result.Plan.Tasks[0].Train == nil || p.Tasks[0].Activities != nil {
					t.Fatal("unrelated selection or original plan mutated")
				}
				for _, q := range result.Queries {
					if q.TaskID == "t1" {
						t.Fatal("activity change queried unchanged train")
					}
				}
			case "date_change":
				if result.Plan.Tasks[0].Train != nil || p.Tasks[0].Train == nil {
					t.Fatal("date change failed to invalidate only new result")
				}
			case "unknown_price":
				if result.Validation.Status == "complete" {
					t.Fatal("unknown price treated as budget satisfied")
				}
				if result.Plan.Tasks[1].Train == nil || result.Plan.Tasks[1].Train.Candidate.PriceFen != nil {
					t.Fatal("candidate facts lost or unknown price became zero")
				}
				found := false
				for _, issue := range result.Validation.Issues {
					if issue.Code == "budget_unknown" {
						found = true
					}
				}
				if !found {
					t.Fatal("missing explicit budget uncertainty")
				}
			case "budget_exceeded", "task_budget_exceeded":
				if result.Validation.Status != "conflict" {
					t.Fatalf("hard conflict masked by later partial status: %+v", result.Validation)
				}
			}
		})
	}
}
