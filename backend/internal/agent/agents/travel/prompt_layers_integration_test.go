package travel

import (
	"context"
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func assertRuntimeLayers(t *testing.T, input []*schema.Message, role string) {
	t.Helper()
	if len(input) == 0 || !strings.HasPrefix(input[0].Content, "[travel-prompt:role:"+role+"]") {
		t.Fatalf("actual input did not use %s runtime prefix", role)
	}
	joined := ""
	for _, msg := range input {
		if msg.Role == schema.System {
			joined += msg.Content + "\n"
		}
	}
	if strings.Count(joined, "[travel-prompt:role:") != 1 || strings.Count(joined, "[travel-prompt:reply]") != 1 {
		t.Fatal("actual input has duplicate role/common layer")
	}
	for _, old := range []string{instruction, reviewInstruction, correctionInstruction, interactionInstruction, planningSummaryInstruction} {
		if old != "" && strings.Contains(joined, old) {
			t.Fatal("legacy whole prompt remains active")
		}
	}
	if (role == "summary" || role == "stopped") && strings.Contains(joined, "[travel-prompt:planning]") {
		t.Fatal("planning actions in non-planning role")
	}
}

type prefixCaptureModel struct{ inputs [][]*schema.Message }

func (m *prefixCaptureModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *prefixCaptureModel) Generate(_ context.Context, in []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, in)
	return schema.AssistantMessage("已根据现有结果整理。", nil), nil
}
func (m *prefixCaptureModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

func TestTravelPromptLayersIntegration(t *testing.T) {
	for _, mode := range []string{"normal", "lookup"} {
		t.Run(mode, func(t *testing.T) {
			m, _, _, _ := runInteractionScenario(t, mode, &interactionMemory{}, false, false)
			if len(m.inputs) != 2 {
				t.Fatal("normal path added model call")
			}
			assertRuntimeLayers(t, m.inputs[0], "main")
			role := "planning_summary"
			if mode == "lookup" {
				role = "summary"
			}
			assertRuntimeLayers(t, m.inputs[1], role)
		})
	}
	for _, stopped := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_flow", true: "stopped"}[stopped], func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			model := &prefixCaptureModel{}
			cfg := KernelConfig{Model: model, Registry: registry, MaxIterations: 8}
			if stopped {
				flow := newSourceFlow(flowConfig{Trips: trips, OriginalQuery: "现有查询结果"})
				flow.registry = registry
				cfg.NativeBeforeModel = func(ctx context.Context, in []*schema.Message) ([]*schema.Message, string, error) {
					s, err := flow.load(ctx)
					if err != nil {
						return nil, "", err
					}
					s.StopReason = "turn_deadline_exceeded"
					if err = flow.save(ctx, s); err != nil {
						return nil, "", err
					}
					return flow.beforeModel(ctx, in)
				}
			}
			kernel, err := NewKernel(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "查看现有结果")
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			if len(model.inputs) != 1 {
				t.Fatal("unexpected model calls")
			}
			role := "main"
			if stopped {
				role = "stopped"
			}
			assertRuntimeLayers(t, model.inputs[0], role)
		})
	}
}
