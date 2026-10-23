package memory

import (
	"context"
	"strings"
	"testing"

	"agent-platform/backend/internal/model"
)

type summaryFixture struct{}

func (summaryFixture) ListTravelSummaries(context.Context, model.Scope, string, string) ([]model.TravelSummary, error) {
	return []model.TravelSummary{{Content: strings.Repeat("摘要", 1000)}}, nil
}

func TestTravelContextBudget(t *testing.T) {
	old := []model.Message{
		{RunID: "old", Role: "user", Content: strings.Repeat("x", 200)},
		{RunID: "old", Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call", Name: "query_travel", Arguments: "{}"}}},
		{RunID: "old", Role: "tool", ToolCallID: "call", Content: "actual evidence"},
	}
	current := model.Message{RunID: "new", Role: "user", Content: "新的任务"}
	input := append(old, current)
	got, err := NewTravelContext(nil, summaryFixture{}, 160).Build(context.Background(), model.Scope{}, "", "new", input)
	if err != nil || len(got) != 1 || got[0].RunID != "new" {
		t.Fatalf("optional summaries must not crowd out the current task: %+v %v", got, err)
	}
	got, err = NewTravelContext(nil, nil, 160).Build(context.Background(), model.Scope{}, "", "new", input)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.RunID == "old" {
			t.Fatal("oversized old run was split")
		}
	}
	if _, err = NewTravelContext(nil, nil, 1).Build(context.Background(), model.Scope{}, "", "new", input); err == nil {
		t.Fatal("oversized current task must return an explicit limit")
	}
}
