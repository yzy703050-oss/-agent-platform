package travel

import (
	"strings"
	"testing"
)

func TestTravelCompletedSelectionWithoutReplyFinishes(t *testing.T) {
	m, _, trips, final := runInteractionScenario(t, "normal", &interactionMemory{}, false, true)
	if len(m.inputs) != 2 || trips.writes != 2 || trips.queries != 1 || !strings.Contains(final, "G1") {
		t.Fatalf("explicitly finished selection needed another model: calls=%d writes=%d queries=%d final=%q", len(m.inputs), trips.writes, trips.queries, final)
	}
}

func TestTravelNormalPlanningTwoCalls(t *testing.T) {
	for _, mode := range []string{"normal", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			m, _, trips, final := runInteractionScenario(t, mode, &interactionMemory{rows: nil}, false, false)
			want := 1
			if mode == "multiple" {
				want = 2
			}
			if len(m.inputs) != 2 || trips.writes != 2*want || trips.queries != want || !strings.Contains(final, "G1") {
				t.Fatalf("calls=%d writes=%d queries=%d final=%q", len(m.inputs), trips.writes, trips.queries, final)
			}
		})
	}
}
