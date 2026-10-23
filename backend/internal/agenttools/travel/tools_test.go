package travel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type deadlineTool struct{}

func (deadlineTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "query_policy"}, nil
}
func (deadlineTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func TestTravelToolBoundaries(t *testing.T) {
	t.Run("policy_timeout_is_unverified", func(t *testing.T) {
		tool := &observedTool{InvokableTool: deadlineTool{}, name: "query_policy", timeout: time.Millisecond}
		out, err := tool.InvokableRun(context.Background(), `{"query":"政策"}`)
		if err != nil {
			t.Fatal(err)
		}
		var result model.PolicyAssessment
		if json.Unmarshal([]byte(out), &result) != nil || result.Compliance != "unverified" {
			t.Fatalf("timeout produced unsafe policy result: %s", out)
		}
	})
	t.Run("model_cannot_override_scope", func(t *testing.T) {
		called := false
		tool, err := infer("read_travel", "scope guard", time.Second, func(context.Context, *readArgs) (*model.TripPlan, error) {
			called = true
			return &model.TripPlan{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := tool.InvokableRun(context.Background(), `{"plan_id":"p","user_id":"another-user"}`)
		if err != nil || called || !strings.Contains(out, "invalid_input") {
			t.Fatalf("untrusted scope passed through: %s %v", out, err)
		}
	})
}
