package travel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agenttools"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type measuredFixture struct{ calls int }

func (m *measuredFixture) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *measuredFixture) Generate(context.Context, []*schema.Message, ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.calls == 6 {
		return nil, errors.New("provider failed with sensitive detail")
	}
	out := schema.AssistantMessage("ok", nil)
	if m.calls != 2 {
		out.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}}
	}
	return out, nil
}
func (m *measuredFixture) Stream(context.Context, []*schema.Message, ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not used")
}

func TestTravelMetricsAccounting(t *testing.T) {
	rows := []ModelCallMeasurement{}
	ctx := agenttools.WithObserver(context.Background(), func(e contracts.Event) error {
		if e.Type != "travel.model_call" {
			return nil
		}
		var row ModelCallMeasurement
		if err := json.Unmarshal(e.Payload, &row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	wrapped := measureModel(&measuredFixture{})
	bound, err := wrapped.WithTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"agent:main", "agent:trip_review", "agent:travel_policy", "agent:preference_analysis", "main:summary", "agent:main"} {
		_, _ = bound.Generate(withModelStage(ctx, stage), []*schema.Message{schema.UserMessage("test")})
	}
	counts := map[string]int{}
	tokens, missing, failed := 0, 0, 0
	for _, row := range rows {
		counts[row.Stage]++
		if row.Usage == nil {
			missing++
		} else {
			tokens += row.Usage.TotalTokens
		}
		if row.ErrorCode != "" {
			failed++
		}
		if row.CallID == "" || row.FinishedAt.Before(row.StartedAt) || row.DurationMS < 0 {
			t.Fatal("incomplete measurement", row)
		}
	}
	sum := 0
	for _, n := range counts {
		sum += n
	}
	if len(rows) != 6 || sum != 6 || len(counts) != 5 || tokens != 120 || missing != 2 || failed != 1 {
		t.Fatal("missing usage/failure/stage accounting", len(rows), counts, tokens, missing, failed)
	}
}
