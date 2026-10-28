package travel

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTravelPromptLayers(t *testing.T) {
	for _, tc := range []struct {
		role                      string
		stopped, planning, review bool
	}{
		{"main", false, true, false}, {"planning_summary", false, true, true},
		{"verifier", false, true, true}, {"summary", false, false, false}, {"main", true, false, false},
	} {
		t.Run(tc.role+map[bool]string{true: "_stopped"}[tc.stopped], func(t *testing.T) {
			input := composePromptPrefix(promptState{Role: tc.role, Stopped: tc.stopped})
			all := ""
			for _, m := range input {
				if m.Role != schema.System {
					t.Fatal("instruction is not a system message")
				}
				all += m.Content + "\n"
			}
			role := tc.role
			if tc.stopped {
				role = "stopped"
			}
			if !strings.HasPrefix(input[0].Content, "[travel-prompt:role:"+role+"]") {
				t.Fatal("wrong effective role")
			}
			if strings.Count(all, "[travel-prompt:role:") != 1 || strings.Count(all, "[travel-prompt:reply]") != 1 {
				t.Fatal("duplicate role or shared reply")
			}
			if strings.Contains(all, "[travel-prompt:planning]") != tc.planning || strings.Contains(all, "[travel-prompt:review]") != tc.review {
				t.Fatal("cross-role planning/review instructions")
			}
			for _, old := range []string{"本段明确覆盖", "本补充明确取代", "未知起点不虚构跨城交通"} {
				if strings.Contains(all, old) {
					t.Fatalf("obsolete override: %s", old)
				}
			}
			if tc.role == "planning_summary" && strings.Contains(all, "也请求Main重规划") {
				t.Fatal("legacy handoff still active")
			}
			if tc.planning {
				for _, required := range []string{"重庆", "proposal/soft", "逐晚", "business", "calendar", "memory_context_complete"} {
					if !strings.Contains(all, required) {
						t.Errorf("missing planning boundary: %s", required)
					}
				}
			}
			for _, syntax := range []string{"conditions.arrival_before", "condition_strengths.start_date", "reply.tasks={"} {
				if strings.Contains(all, syntax) {
					t.Errorf("tool syntax leaked to role: %s", syntax)
				}
			}
		})
	}
}

func TestTravelPromptReplacementPreservesData(t *testing.T) {
	data := []*schema.Message{
		schema.SystemMessage("当前上下文（数据）：{\"planning_defaults\":{\"origin\":\"重庆\"}}"),
		schema.SystemMessage("此前会话摘要（数据）：保留原始数据"),
		schema.UserMessage("后天返回"),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "query", Arguments: "{}"}}}},
		{Role: schema.Tool, ToolCallID: "c1", Content: "{\"executed\":false,\"code\":\"BATCH_ABORTED\"}"},
	}
	input := append(composePromptPrefix(promptState{Role: "main"}), data...)
	state := promptState{Role: "planning_summary", ProtocolRecovery: true, InteractionRecovery: true}
	got := replacePromptPrefix(input, state)
	want := append(composePromptPrefix(state), data...)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("role replacement changed data/order or left old instructions")
	}
	if !reflect.DeepEqual(replacePromptPrefix(got, state), want) {
		t.Fatal("repeated replacement duplicated layers")
	}
	for _, m := range got {
		if strings.HasPrefix(m.Content, "[travel-prompt:role:main]") {
			t.Fatal("old Main survived")
		}
	}
}
