package travel

import (
	"encoding/json"
	"testing"

	pm "agent-platform/backend/internal/model"
)

func TestTravelHardStaySurvivesReplan(t *testing.T) {
	s := FlowState{}
	p := pm.TripPlan{ID: "p", Tasks: []pm.TripTask{{Destination: "杭州", RequiresHotel: true, Nights: 1, FieldSources: map[string]string{"nights": "user"}, ConditionStrengths: map[string]pm.ConstraintStrength{"nights": pm.Hard}}}}
	s.observeHardStay(p)
	p.Tasks[0].Nights = 2
	s.observeHardStay(p)
	raw, _ := json.Marshal(s)
	var restored FlowState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.HardStayNights["p"]["杭州"] != 1 {
		t.Fatal("replan overwrote original hard stay", restored.HardStayNights)
	}
	p.ID = "soft"
	p.Tasks[0].ConditionStrengths["nights"] = pm.Soft
	s.observeHardStay(p)
	if len(s.HardStayNights["soft"]) != 0 {
		t.Fatal("soft/default stay was locked")
	}
}
