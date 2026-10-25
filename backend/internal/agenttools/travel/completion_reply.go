package travel

import (
	"agent-platform/backend/internal/model"
	"slices"
)

// Omitting reply can mean further semantic repair is intended. Only an
// explicit stop plus an exact, complete receipt permits deterministic binding.
func completedStop(call PreparedCall, saved model.TripResult, operationID string) bool {
	cmd := call.Trip
	if cmd == nil || cmd.AllowRepair == nil || *cmd.AllowRepair || call.Reply != nil || (cmd.Operation != "select" && cmd.Operation != "amend") || saved.Operation != cmd.Operation || saved.Plan.ID != cmd.PlanID || len(saved.Queries) > 0 {
		return false
	}
	if saved.Receipt == nil || saved.Receipt.OperationID != operationID || saved.Receipt.PlanID != saved.Plan.ID || saved.Receipt.Revision != saved.Plan.Revision {
		return false
	}
	for _, v := range []model.TripValidation{saved.Validation, saved.Plan.Validation} {
		if v.Status != "complete" || slices.ContainsFunc(v.Issues, func(issue model.TripIssue) bool { return issue.Blocking }) {
			return false
		}
	}
	return true
}
