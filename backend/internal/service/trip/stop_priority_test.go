package trip

import (
	"reflect"
	"testing"

	"agent-platform/backend/internal/model"
)

func TestSourceStopPriority(t *testing.T) {
	no := false
	for _, tc := range []struct {
		name, operation, issue, status, want string
		closed, hotel, dates                 bool
	}{
		{"policy_select_closed", "select", "policy_unverified", "partial", "policy_unverified", true, false, false},
		{"policy_confirm_closed", "confirm", "policy_constraint_unverified", "partial", "policy_unverified", true, false, false},
		{"hotel_closed", "select", "hotel_price_unknown", "partial", "provider_capability_missing", true, true, false},
		{"hard_dates_closed", "select", "hard_date_conflict", "partial", "hard_constraint_conflict", true, false, true},
		{"hard_selection_closed", "select", "hard_budget_conflict", "partial", "repair_disabled", true, false, false},
		{"hard_selection_open", "select", "hard_budget_conflict", "partial", "hard_constraint_conflict", false, false, false},
		{"hard_proposal_open", "propose", "hard_budget_conflict", "partial", "awaiting_model", false, false, false},
		{"pending_closed", "select", "train_pending", "partial", "repair_disabled", true, false, false},
		{"pending_open", "propose", "train_pending", "partial", "awaiting_model", false, false, false},
		{"missing_input_is_validation_not_stop_override", "propose", "train_pending", "needs_input", "awaiting_model", false, false, false},
		{"pause", "pause", "policy_unverified", "partial", "paused", true, false, false},
		{"cancel", "cancel", "hard_budget_conflict", "partial", "cancelled", true, false, false},
		{"valid_confirm", "confirm", "", "complete", "", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := model.TripPlan{SourceVersion: 1, Validation: model.TripValidation{Status: tc.status}}
			if tc.issue != "" {
				p.Validation.Issues = []model.TripIssue{{Code: tc.issue, Blocking: true}}
			}
			if tc.hotel {
				p.Tasks = []model.TripTask{{Hotel: &model.TravelSelection{Candidate: model.TravelCandidate{Name: "酒店地点"}}}}
			}
			if tc.dates {
				p.ConfirmedConditions = sourceConditions(`{"start_date":"2026-11-02","end_date":"2026-11-01"}`)
			}
			before, _ := clone(p.Validation)
			cmd := model.TripCommand{Operation: tc.operation, Reason: "用户要求"}
			if tc.closed {
				cmd.AllowRepair = &no
			}
			finishSourcePlan(&p, cmd)
			if p.StopReason != tc.want {
				t.Errorf("stop=%q want=%q", p.StopReason, tc.want)
			}
			if !reflect.DeepEqual(before, p.Validation) {
				t.Fatal("stop classification changed validation")
			}
			if tc.status != "complete" && p.Status == "completed" {
				t.Fatal("invalid plan committed by stop classification")
			}
			if tc.status == "complete" && (p.Status != "completed" || !p.UserConfirmed) {
				t.Fatal("valid confirmation lost")
			}
		})
	}
}
