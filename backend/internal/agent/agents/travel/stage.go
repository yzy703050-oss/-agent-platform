package travel

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/adk"
)

type modelStageKey struct{}

func withModelStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, modelStageKey{}, stage)
}
func modelStage(ctx context.Context) string {
	if stage, ok := ctx.Value(modelStageKey{}).(string); ok {
		return stage
	}
	return flowModelStage(ctx)
}
func flowModelStage(ctx context.Context) string {
	if raw, ok := adk.GetSessionValue(ctx, flowKey); ok {
		if text, ok := raw.(string); ok {
			var s struct {
				Role string `json:"role"`
			}
			if json.Unmarshal([]byte(text), &s) == nil {
				switch s.Role {
				case "verifier":
					return "agent:trip_review"
				case "summary":
					return "agent:summary"
				case "planning_summary":
					return "agent:trip_summary"
				}
			}
		}
	}
	return "agent:main"
}
