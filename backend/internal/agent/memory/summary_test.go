package memory

import (
	"context"
	"fmt"
	"testing"

	"agent-platform/backend/internal/model"
)

type maintenanceFixture struct {
	summaries []model.TravelSummary
	writes    []model.TravelPreferenceCommand
}

func (f *maintenanceFixture) ListTravelSummaries(context.Context, model.Scope, string, string) ([]model.TravelSummary, error) {
	return f.summaries, nil
}
func (f *maintenanceFixture) SaveTravelSummary(_ context.Context, _ model.TravelExecution, s model.TravelSummary) (model.TravelSummary, error) {
	f.summaries = append(f.summaries, s)
	return s, nil
}
func (f *maintenanceFixture) Apply(_ context.Context, _ model.TravelExecution, _ string, c model.TravelPreferenceCommand) (model.TravelPreference, error) {
	f.writes = append(f.writes, c)
	return model.TravelPreference{}, nil
}
func TestTravelMemoryMaintenance(t *testing.T) {
	ctx := context.Background()
	f := &maintenanceFixture{}
	m := NewMaintenance(f, f)
	ref := model.TravelExecution{RunID: "current"}
	var history []model.Message
	for i := 0; i < 5; i++ {
		history = append(history, model.Message{RunID: fmt.Sprint(i), Role: "user", Content: "我习惯坐二等座"})
	}
	if err := m.Maintain(ctx, ref, history[:4]); err != nil || len(f.writes) != 0 {
		t.Fatalf("analysis ran too often: %v", err)
	}
	if err := m.Maintain(ctx, ref, history); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 || f.writes[0].Origin != "inferred" || f.writes[0].Value != "二等座" || len(f.summaries) != 1 || len(f.summaries[0].SourceRuns) != 5 {
		t.Fatalf("missing bounded source-tracked analysis: %+v", f)
	}
	if err := m.Maintain(ctx, ref, history); err != nil || len(f.writes) != 1 {
		t.Fatalf("unchanged history reanalyzed: %v", err)
	}
	// Tool text and current, unfinished user requests are never inference input.
	g := &maintenanceFixture{}
	for i := range history {
		history[i].Role = "tool"
	}
	if err := NewMaintenance(g, g).Maintain(ctx, ref, history); err != nil || len(g.writes) != 0 {
		t.Fatalf("tool text inferred a preference: %v", err)
	}
}
