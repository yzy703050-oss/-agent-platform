package trip

import (
	"agent-platform/backend/internal/model"
	"testing"
)

func TestSourceHotelPolicyCapabilityGap(t *testing.T) {
	for _, tc := range []struct {
		name, extra, rule string
		priced, stop      bool
	}{
		{name: "hotel_policy_missing_quote", rule: "hotel_max_nightly_cny", stop: true},
		{name: "unrelated_policy", rule: "train_max_total_cny"},
		{name: "policy_itself_unverified", rule: "hotel_max_nightly_cny", extra: "policy_unverified"},
		{name: "train_still_missing", rule: "hotel_max_nightly_cny", extra: "train_pending"},
		{name: "hard_conflict", rule: "hotel_max_nightly_cny", extra: "hard_budget_conflict"},
		{name: "quote_exists", rule: "hotel_max_nightly_cny", priced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := int64(30000)
			p := model.TripPlan{ID: "p", SourceVersion: 1, Tasks: []model.TripTask{{ID: "t", RequiresHotel: true, Hotel: &model.TravelSelection{Candidate: model.TravelCandidate{ID: "hotel", Name: "酒店地点", City: "杭州"}}}}, PolicyContext: &model.SourcePolicyContext{Status: "ok", Constraints: []model.SourcePolicyRule{{ID: "rule", Field: tc.rule}}}, Validation: model.TripValidation{Status: "partial", Issues: []model.TripIssue{{Code: "hotel_price_unknown", TaskID: "t", Blocking: true}, {Code: "policy_constraint_unverified", TaskID: "t", Blocking: true, Evidence: rawValue("rule")}}}}
			if tc.extra != "" {
				p.Validation.Issues = append(p.Validation.Issues, model.TripIssue{Code: tc.extra, Blocking: true})
			}
			if tc.priced {
				p.Tasks[0].Hotel.Candidate.PriceFen = &price
			}
			finishSourcePlan(&p, model.TripCommand{Operation: "select"})
			if (p.StopReason == "provider_capability_missing") != tc.stop {
				t.Fatalf("stop=%s expected capability=%v", p.StopReason, tc.stop)
			}
		})
	}
}
