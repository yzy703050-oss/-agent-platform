package travel

import (
	"context"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type protocolModel struct {
	calls, invalid int
	regeneration   bool
}

func (m *protocolModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) { return m, nil }
func (m *protocolModel) Generate(_ context.Context, messages []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls++
	for _, msg := range messages {
		if msg.Content == regenerationInstruction {
			m.regeneration = true
		}
	}
	if m.calls <= m.invalid {
		return &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: "length"}}, nil
	}
	return schema.AssistantMessage("已理解请求。", nil), nil
}
func (m *protocolModel) Stream(ctx context.Context, messages []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, messages, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), err
}
func TestTravelSourceModelRegeneration(t *testing.T) {
	for _, invalid := range []int{2, 3} {
		t.Run(map[int]string{2: "complete_response_after_two_failures", 3: "source_retry_budget_exhausted"}[invalid], func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, native: true}
			m := &protocolModel{invalid: invalid}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, Handlers: []adk.ChatModelAgentMiddleware{h}})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "请求")
			failed := false
			for {
				e, ok := it.Next()
				if !ok {
					break
				}
				failed = failed || e.Err != nil
			}
			if m.calls != 3 || !m.regeneration || failed != (invalid == 3) || len(store.results) != 0 || trips.writes.Load() != 0 {
				t.Fatal(m.calls, m.regeneration, failed, len(store.decisions))
			}
			want := 1
			if invalid == 3 {
				want = 0
			}
			if len(store.decisions) != want {
				t.Fatal("incomplete model response was accepted as a decision")
			}
		})
	}
}

func TestTravelSourceContextResponseBudget(t *testing.T) {
	ctx := context.Background()
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: &sourceGateTrips{}}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := registry.Profile(traveltools.ProfileRequest{Role: "main"})
	if err != nil {
		t.Fatal(err)
	}
	input := []*schema.Message{schema.SystemMessage(instruction), schema.SystemMessage(replyPolicy), schema.UserMessage("请求")}
	store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
	h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, native: true, contextTokens: sourceTokenEstimate(input, tools) + 5}
	m := &sourceGateModel{}
	kernel, err := NewKernel(ctx, KernelConfig{Model: m, Registry: registry, MaxIterations: 8, Handlers: []adk.ChatModelAgentMiddleware{h}})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "请求")
	failed := false
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		failed = failed || event.Err != nil
	}
	if !failed || len(store.decisions) != 0 {
		t.Fatal("over-budget model output was accepted before checking complete input+output")
	}
}
