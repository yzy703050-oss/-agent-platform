package travel

import (
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type completedReplyTrips struct {
	sourceTripsFixture
	result model.TripResult
}

func (f *completedReplyTrips) Apply(context.Context, model.TravelExecution, string, model.TripCommand) (model.TripResult, error) {
	return f.result, nil
}

func TestCompletedExplicitStopReply(t *testing.T) {
	for _, name := range []string{"complete", "repair_allowed", "repair_unspecified", "partial", "blocking_issue", "pending_query", "propose", "bad_receipt", "wrong_plan", "wrong_operation"} {
		t.Run(name, func(t *testing.T) {
			stop := false
			validation := model.TripValidation{Status: "complete"}
			saved := model.TripResult{Operation: "select", Plan: model.TripPlan{ID: "p", Revision: 2, Status: "completed", Validation: validation, Tasks: []model.TripTask{{ID: "t", Origin: "上海", Destination: "杭州", Train: &model.TravelSelection{Candidate: model.TravelCandidate{Name: "G1"}}}}}, Validation: validation, Receipt: &model.TravelReceipt{OperationID: "select-op", PlanID: "p", Revision: 2}}
			call := PreparedCall{NativeCall: NativeCall{ID: "select", Name: "write_trip"}, Trip: &model.TripCommand{Operation: "select", PlanID: "p", AllowRepair: &stop}}
			switch name {
			case "repair_allowed":
				stop = true
			case "repair_unspecified":
				call.Trip.AllowRepair = nil
			case "partial":
				saved.Validation.Status = "partial"
				saved.Plan.Validation.Status = "partial"
			case "blocking_issue":
				saved.Validation.Issues = []model.TripIssue{{Code: "goal_not_satisfied", Blocking: true}}
			case "pending_query":
				saved.Queries = []model.TravelQuery{{Domain: "train"}}
			case "propose":
				saved.Operation = "propose"
				call.Trip.Operation = "propose"
			case "bad_receipt":
				saved.Receipt.Revision = 1
			case "wrong_plan":
				call.Trip.PlanID = "other"
			case "wrong_operation":
				call.Trip.Operation = "amend"
			}
			registry, err := NewRegistry(NativeConfig{Config: Config{Trips: &completedReplyTrips{result: saved}}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := registry.Execute(WithOperationID(context.Background(), "select-op"), call)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				RenderedAnswer string `json:"rendered_answer"`
			}
			if err = json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if name == "complete" {
				if !strings.Contains(result.RenderedAnswer, "G1") {
					t.Fatal("explicitly finished selection lacks durable rendered reply", out)
				}
			} else if result.RenderedAnswer != "" {
				t.Fatal("unsafe or unfinished mutation was finalized", name, out)
			}
		})
	}
}
