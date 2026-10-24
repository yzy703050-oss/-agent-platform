package memory

import (
	"context"
	"strings"
	"testing"

	"agent-platform/backend/internal/model"
)

func TestTravelContextSourceCompaction(t *testing.T) {
	history := []model.Message{}
	for _, id := range []string{"one", "two", "three"} {
		history = append(history, model.Message{RunID: id, Role: "user", Content: strings.Repeat("x", 1000)}, model.Message{RunID: id, Role: "assistant", Content: strings.Repeat("y", 1000)})
	}
	history = append(history, model.Message{RunID: "current", Role: "user", Content: "当前请求"}, model.Message{RunID: "current", Role: "assistant", ToolCalls: []model.ToolCall{{ID: "a", Name: "query", Arguments: `{}`}}}, model.Message{RunID: "current", Role: "tool", ToolCallID: "a", Content: "当前完整工具结果"})
	calls := 0
	summarize := func(_ context.Context, previous string, messages []model.Message) (string, error) {
		calls++
		if len(messages) != 2 || messages[0].RunID == "current" {
			t.Fatal("not a complete old prefix", messages)
		}
		return "来源受限摘要", nil
	}
	estimate := func(summary string, messages []model.Message) int {
		size := len(summary)
		for _, msg := range messages {
			size += len(msg.Content)
		}
		return size
	}
	result, err := PrepareSourceContext(context.Background(), history, "current", 5000, "", nil, estimate, summarize)
	if err != nil || calls != 2 || len(result.SourceRuns) != 2 || result.Messages[len(result.Messages)-1].ToolCallID != "a" {
		t.Fatal(calls, result, err)
	}
	// No compression below 80%, and an incomplete old pair is never summarized.
	calls = 0
	if _, err = PrepareSourceContext(context.Background(), history, "current", 10000, "", nil, estimate, summarize); err != nil || calls != 0 {
		t.Fatal(calls, err)
	}
	broken := append([]model.Message(nil), history...)
	broken[1] = model.Message{RunID: "one", Role: "assistant", ToolCalls: []model.ToolCall{{ID: "unpaired", Name: "query", Arguments: `{}`}}}
	calls = 0
	if _, err = PrepareSourceContext(context.Background(), broken, "current", 1000, "", nil, estimate, summarize); err == nil || calls != 0 {
		t.Fatal("incomplete prefix compressed or budget ignored", calls, err)
	}
}
