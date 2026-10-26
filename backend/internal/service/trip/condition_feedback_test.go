package trip

import (
	"strings"
	"testing"

	"agent-platform/backend/internal/model"
)

func TestTravelHardConditionErrorNamesField(t *testing.T) {
	task := model.TripTask{ConditionStrengths: map[string]model.ConstraintStrength{"passengers": model.Hard}}
	err := applyChange(&task, model.TripTaskChange{ConditionStrengths: map[string]model.ConstraintStrength{"passengers": model.Soft}}, "main")
	if err == nil || !strings.Contains(err.Error(), "condition_strengths.passengers") {
		t.Fatalf("hard metadata rejection hides offending field: %v", err)
	}
}
