package travel

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type sourceGateTrips struct{ writes atomic.Int64 }

type sourceGateIdentity struct {
	*adk.BaseChatModelAgentMiddleware
}

func (h *sourceGateIdentity) WrapInvokableToolCall(_ context.Context, next adk.InvokableToolCallEndpoint, t *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		return next(traveltools.WithOperationID(ctx, "accepted-"+t.CallID), args, opts...)
	}, nil
}

func (f *sourceGateTrips) Apply(_ context.Context, _ pm.TravelExecution, id string, cmd pm.TripCommand) (pm.TripResult, error) {
	if cmd.Context == nil || cmd.Context.Role != "main" {
		return pm.TripResult{}, pm.ErrInvalidInput
	}
	f.writes.Add(1)
	p := pm.TripPlan{ID: "p", Revision: 2, SourceVersion: 1, Status: "completed", Validation: pm.TripValidation{Status: "complete"}, Tasks: []pm.TripTask{{ID: "t", Origin: "上海", Destination: "杭州", Activities: []pm.TripActivity{{Purpose: "会议", Date: "2026-10-26", Time: "16:00", Source: "user", Strength: pm.Hard}}}}}
	return pm.TripResult{Operation: cmd.Operation, Plan: p, Validation: p.Validation, Receipt: &pm.TravelReceipt{OperationID: id, PlanID: p.ID, Revision: p.Revision}}, nil
}
func (*sourceGateTrips) Read(context.Context, pm.Scope, string) (pm.TripPlan, error) {
	return pm.TripPlan{}, nil
}
func (*sourceGateTrips) Inspect(context.Context, pm.TravelExecution, string) (pm.TripResult, error) {
	return pm.TripResult{}, nil
}
func (*sourceGateTrips) Query(context.Context, pm.TravelExecution, pm.TravelQuery) (pm.TravelEvidence, error) {
	return pm.TravelEvidence{}, nil
}
func (*sourceGateTrips) Prepare(context.Context, pm.TravelExecution, pm.TripCommand) (pm.TripResult, error) {
	return pm.TripResult{}, nil
}

type sourceGateModel struct {
	calls       atomic.Int64
	invalid     bool
	paired      bool
	independent bool
	unknown     bool
	pretty      bool
}

func (m *sourceGateModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *sourceGateModel) Generate(_ context.Context, messages []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	if m.calls.Add(1) > 1 {
		results := map[string]bool{}
		for _, msg := range messages {
			if msg.Role == schema.Tool {
				var out pm.TravelToolResult
				if json.Unmarshal([]byte(msg.Content), &out) == nil && out.Status == "rejected" {
					results[msg.ToolCallID] = true
				}
			}
		}
		m.paired = results["a"] && results["b"]
		return schema.AssistantMessage("已收到整批拒绝原因。", nil), nil
	}
	msg := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "a", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":1},"reply":{}}`}}}}
	if m.invalid {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{ID: "b", Type: "function", Function: schema.FunctionCall{Name: "read", Arguments: `{"request_kind":"planning","resource":"trip","user_id":"forged"}`}})
	}
	if m.independent {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{ID: "b", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"q","expected_revision":1}}`}})
	}
	if m.unknown {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{ID: "b", Type: "function", Function: schema.FunctionCall{Name: "unregistered", Arguments: `{}`}})
	}
	if m.pretty {
		for i := range msg.ToolCalls {
			var out bytes.Buffer
			_ = json.Indent(&out, []byte(msg.ToolCalls[i].Function.Arguments), "", "  ")
			msg.ToolCalls[i].Function.Arguments = out.String()
		}
	}
	return msg, nil
}

type sourceJournalStore struct {
	mu        sync.Mutex
	decisions map[string]pm.TravelDecision
	results   map[string]string
}

func (s *sourceJournalStore) FindTravelDecision(_ context.Context, _ pm.TravelExecution, key string) (pm.TravelDecision, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decisions[key]
	return d, ok, nil
}
func (s *sourceJournalStore) AcceptTravelDecision(_ context.Context, _ pm.TravelExecution, d pm.TravelDecision) (pm.TravelDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.OperationIDs = map[string]string{}
	for _, c := range d.Response.ToolCalls {
		d.OperationIDs[c.ID] = d.DecisionKey + "/" + c.ID
	}
	s.decisions[d.DecisionKey] = d
	return d, nil
}
func (s *sourceJournalStore) FindTravelToolResult(_ context.Context, _ pm.TravelExecution, id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.results[id]
	return v, ok, nil
}
func (s *sourceJournalStore) SaveTravelToolResult(_ context.Context, _ pm.TravelExecution, id, out string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[id] = out
	return nil
}

func TestTravelSourceJournal(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent_writes", true: "unknown_call_is_durably_rejected"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			trips := &sourceGateTrips{}
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			store := &sourceJournalStore{decisions: map[string]pm.TravelDecision{}, results: map[string]string{}}
			h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store, native: true}
			m := &sourceGateModel{independent: !unknown, unknown: unknown, pretty: true}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, MaxIterations: 8, Registry: registry, Handlers: []adk.ChatModelAgentMiddleware{h}, NativeRejected: h.recordRejected})
			if err != nil {
				t.Fatal(err)
			}
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "核对两个行程")
			for {
				e, ok := it.Next()
				if !ok {
					break
				}
				if e.Err != nil {
					t.Fatal(e.Err)
				}
			}
			if len(store.results) != 2 {
				t.Fatalf("durable results=%d want 2", len(store.results))
			}
			want := int64(2)
			if unknown {
				want = 0
			}
			if trips.writes.Load() != want {
				t.Fatalf("writes=%d want %d", trips.writes.Load(), want)
			}
		})
	}
}
func (m *sourceGateModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}

func TestTravelEinoBatchGate(t *testing.T) {
	t.Run("source_safe_point_reply_at_model_budget", func(t *testing.T) {
		ctx := context.Background()
		registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: &sourceGateTrips{}}})
		if err != nil {
			t.Fatal(err)
		}
		m := &sourceGateModel{}
		kernel, err := NewKernel(ctx, KernelConfig{Model: m, MaxIterations: 1, Registry: registry, Handlers: []adk.ChatModelAgentMiddleware{&sourceGateIdentity{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}}, NativeBeforeModel: func(ctx context.Context, messages []*schema.Message) ([]*schema.Message, string, error) {
			s, err := readState(ctx)
			if err != nil {
				return nil, "", err
			}
			if s.ModelCalls >= 1 {
				return messages, "已按本次实际保存结果收尾。", nil
			}
			return messages, "", nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "保存本次安排")
		final := ""
		for {
			event, ok := it.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				t.Fatal(event.Err)
			}
			if event.Output != nil && event.Output.MessageOutput != nil {
				msg, err := event.Output.MessageOutput.GetMessage()
				if err == nil && msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
					final = msg.Content
				}
			}
		}
		if m.calls.Load() != 1 || final != "已按本次实际保存结果收尾。" {
			t.Fatalf("calls=%d final=%q", m.calls.Load(), final)
		}
	})
	for _, invalid := range []bool{true, false} {
		t.Run(map[bool]string{true: "preflight_before_parallel_effects", false: "bound_reply_without_extra_model_call"}[invalid], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			trips := &sourceGateTrips{}
			registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			m := &sourceGateModel{invalid: invalid}
			kernel, err := NewKernel(ctx, KernelConfig{Model: m, MaxIterations: 8, Registry: registry, Handlers: []adk.ChatModelAgentMiddleware{&sourceGateIdentity{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}}, NativeAfterBatch: func(_ context.Context, _ traveltools.PreparedBatch, results []pm.TravelToolResult) (string, error) {
				if len(results) == 1 && results[0].RenderedAnswer != "" {
					return results[0].RenderedAnswer, nil
				}
				return "", nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel})
			it := runner.Query(ctx, "把会议改成16:00")
			for {
				event, ok := it.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			if invalid {
				if trips.writes.Load() != 0 || !m.paired || m.calls.Load() != 2 {
					t.Fatalf("rejected batch: writes=%d paired=%v models=%d", trips.writes.Load(), m.paired, m.calls.Load())
				}
			} else if trips.writes.Load() != 1 || m.calls.Load() != 1 {
				t.Fatalf("bound reply: writes=%d models=%d", trips.writes.Load(), m.calls.Load())
			}
		})
	}
}
