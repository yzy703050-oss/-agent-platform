package travel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestTravelSourceFlow(t *testing.T) {
	t.Run("roles_policy_lookup_planning_and_replan", func(t *testing.T) {
		s := FlowState{}
		if s.DecideRole() != "main" {
			t.Fatal("first decision must be Main")
		}
		s.Results = append(s.Results, pm.TravelToolResult{Name: "query", Status: "ok"})
		if s.DecideRole() != "summary" {
			t.Fatal("lookup must hand to Summary")
		}
		s.Planning = true
		s.Results = append(s.Results, pm.TravelToolResult{Name: "query_policy", Status: "partial"})
		if s.DecideRole() != "main" {
			t.Fatal("policy observation must continue Main planning")
		}
		s.Plans = map[string]pm.TripResult{"p": {Plan: pm.TripPlan{ID: "p"}}}
		s.Results = append(s.Results, pm.TravelToolResult{Name: "write_trip", Status: "partial", TripPlanID: "p"})
		if s.DecideRole() != "verifier" {
			t.Fatal("saved plan must hand to Verifier")
		}
		s.Results = append(s.Results, pm.TravelToolResult{Name: "request_replan", Status: "ok", TripPlanIDs: []string{"p"}, Reason: "遗漏返程"})
		if s.DecideRole() != "main" {
			t.Fatal("global repair must return to Main")
		}
	})
	t.Run("no_progress_ignores_receipts_and_prose_but_resets_on_real_change", func(t *testing.T) {
		p := pm.TripResult{Plan: pm.TripPlan{ID: "p", Revision: 1, Tasks: []pm.TripTask{{ID: "t", Origin: "上海", Destination: "杭州", Revision: 1, Activities: []pm.TripActivity{{ID: "m", Date: "2026-10-26", Time: "14:00", Purpose: "会议"}}}}}}
		s := FlowState{Plans: map[string]pm.TripResult{"p": p}}
		s.ObserveReview()
		p.Plan.Revision++
		p.Receipt = &pm.TravelReceipt{OperationID: "new-receipt"}
		s.Plans["p"] = p
		s.PreviousRoundSummary = "已再次核对"
		s.ObserveReview()
		if s.UnchangedReviews != 1 || s.StopReason != "" {
			t.Fatal(s.UnchangedReviews, s.StopReason)
		}
		s.ObserveReview()
		if s.StopReason != "no_progress" {
			t.Fatal("unchanged_reviews>=2 must stop", s.StopReason)
		}
		s.StopReason = ""
		p.Plan.Tasks[0].Activities[0].Time = "16:00"
		s.Plans["p"] = p
		s.ObserveReview()
		if s.UnchangedReviews != 0 {
			t.Fatal("real change did not reset progress")
		}
	})
	t.Run("rejected_batches_consume_original_planning_budget", func(t *testing.T) {
		s := FlowState{}
		calls := []traveltools.NativeCall{{ID: "a", Name: "write_trip", Arguments: json.RawMessage(`{"request_kind":"planning","tripplan":{}}`)}}
		for i := 0; i < 8; i++ {
			if err := s.ReserveBatch(calls, 8); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ReserveBatch(calls, 8); err == nil || s.PlanningRounds != 8 {
			t.Fatal("planning rounds reset/overran", s.PlanningRounds, err)
		}
	})
	t.Run("partial_reply_requires_saved_actual_outcome_and_allowed_blockers", func(t *testing.T) {
		allow := false
		batch := traveltools.PreparedBatch{Calls: []traveltools.PreparedCall{{NativeCall: traveltools.NativeCall{ID: "a", Name: "write_trip"}, Trip: &pm.TripCommand{AllowRepair: &allow}}}}
		result := pm.TravelToolResult{CallID: "a", Name: "write_trip", Status: "partial", Operation: "amend", TripPlanID: "p", Revision: 2, StopPlanning: true, StopReason: "repair_disabled", RenderedAnswer: "会议已保存为16:00。", Validation: &pm.TripValidation{Issues: []pm.TripIssue{{Code: "hotel_price_unknown", Blocking: true}}}}
		if got := ReadyReply(batch, []pm.TravelToolResult{result}); !strings.Contains(got, "16:00") {
			t.Fatal("valid partial could not stop", got)
		}
		result.Validation.Issues = append(result.Validation.Issues, pm.TripIssue{Code: "train_missing", Blocking: true})
		if got := ReadyReply(batch, []pm.TravelToolResult{result}); got != "" {
			t.Fatal("unsaved/missing selection falsely ended as success")
		}
	})
}

type sourceFlowTrips struct {
	mu              sync.Mutex
	plans           map[string]*pm.TripPlan
	saved           *pm.TripPlan
	evidence        []pm.TravelEvidence
	queries, writes int
	now             time.Time
	ttl             time.Duration
	expireFirst     bool
}

func (f *sourceFlowTrips) Prepare(_ context.Context, _ pm.TravelExecution, c pm.TripCommand) (pm.TripResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return tripsvc.Preview(f.plans[c.PlanID], c, f.evidence, f.now)
}
func (f *sourceFlowTrips) Apply(ctx context.Context, ref pm.TravelExecution, id string, c pm.TripCommand) (pm.TripResult, error) {
	r, err := f.Prepare(ctx, ref, c)
	if err != nil {
		return r, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if r.Plan.ID == "" {
		r.Plan.ID = fmt.Sprintf("p%d", f.writes)
	}
	r.Plan.UserID = ref.Scope.UserID
	r.Plan.ConversationID = ref.ConversationID
	for i := range r.Queries {
		r.Queries[i].PlanID = r.Plan.ID
		r.Queries[i].PlanRevision = r.Plan.Revision
	}
	r.Receipt = &pm.TravelReceipt{OperationID: id, PlanID: r.Plan.ID, Revision: r.Plan.Revision}
	f.saved = &r.Plan
	if f.plans == nil {
		f.plans = map[string]*pm.TripPlan{}
	}
	f.plans[r.Plan.ID] = &r.Plan
	return r, nil
}
func (f *sourceFlowTrips) Read(_ context.Context, _ pm.Scope, id string) (pm.TripPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.plans[id], nil
}
func (f *sourceFlowTrips) Inspect(_ context.Context, _ pm.TravelExecution, id string) (pm.TripResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return pm.TripResult{Plan: *f.plans[id], Validation: f.plans[id].Validation}, nil
}
func (f *sourceFlowTrips) Query(_ context.Context, ref pm.TravelExecution, q pm.TravelQuery) (pm.TravelEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	depart := f.now.AddDate(0, 0, 2).Add(time.Hour)
	arrive := depart.Add(time.Hour)
	price := int64(10000)
	ttl := f.ttl
	if ttl <= 0 {
		ttl = time.Hour
	}
	e := pm.TravelEvidence{ID: fmt.Sprintf("query-%d", f.queries), UserID: ref.Scope.UserID, ConversationID: ref.ConversationID, PlanID: q.PlanID, TaskID: q.TaskID, ResultRevision: 1, Query: q, Status: "ok", Source: pm.TravelSource{Provider: "fixture", FetchedAt: f.now}, ExpiresAt: f.now.Add(ttl), Candidates: []pm.TravelCandidate{{ID: "g1", Name: "G1", Origin: q.Origin, Destination: q.Destination, TrainKind: "high_speed", SeatClass: "二等座", PriceFen: &price, DepartureAt: &depart, ArrivalAt: &arrive}}}
	f.evidence = append(f.evidence, e)
	if f.expireFirst && f.queries == 1 {
		f.now = f.now.Add(2 * time.Millisecond)
	}
	return e, nil
}

func (f *sourceFlowTrips) PrepareRefresh(_ context.Context, _ pm.TravelExecution, id string) (pm.TripRefreshPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := *f.plans[id]
	p.QueryResults = map[string]pm.TravelEvidence{}
	for _, e := range f.evidence {
		if e.PlanID == id {
			p.QueryResults[e.ID] = e
		}
	}
	return tripsvc.PrepareSourceRefresh(p, f.now)
}

type sourceFlowModel struct {
	calls int
	t     *testing.T
}

func (m *sourceFlowModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *sourceFlowModel) Generate(_ context.Context, messages []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.calls == 1 {
		if messages[0].Content != instruction {
			m.t.Fatal("Main prompt changed")
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "create", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: `{"request_kind":"planning","tripplan":{"operation":"propose","default_round_trip":false,"tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-10-26"},"items":[{"domain":"train"}]}]}}`}}}}, nil
	}
	if m.calls > 2 {
		m.t.Fatal("bound reply consumed another model call")
	}
	if messages[0].Content != reviewInstruction {
		m.t.Fatal("Verifier did not receive original review prompt")
	}
	var review struct {
		OriginalQuery string `json:"original_query"`
		Tripplan      struct {
			ID       string `json:"trip_plan_id"`
			Revision int64  `json:"revision"`
			Tasks    []struct {
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
			} `json:"tasks"`
			Candidates []struct {
				QueryID        string `json:"query_id"`
				ResultRevision int64  `json:"result_revision"`
				Items          []struct {
					ID string `json:"id"`
				} `json:"items"`
			} `json:"candidates"`
		} `json:"tripplan"`
	}
	if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &review); err != nil {
		m.t.Fatal(err)
	}
	p := review.Tripplan
	if review.OriginalQuery != "安排上海到杭州的单程行程" || len(p.Candidates) != 1 || len(p.Tasks) != 1 || len(p.Candidates[0].Items) != 1 {
		m.t.Fatalf("incomplete source review: %+v", review)
	}
	args := map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "select", "trip_plan_id": p.ID, "expected_revision": p.Revision, "tasks": []any{map[string]any{"task_id": p.Tasks[0].ID, "expected_revision": p.Tasks[0].Revision, "selection_refs": map[string]any{"train": pm.TravelCandidateRef{QueryID: p.Candidates[0].QueryID, ResultRevision: p.Candidates[0].ResultRevision, CandidateID: p.Candidates[0].Items[0].ID}}}}}, "reply": map[string]any{}}
	raw, _ := json.Marshal(args)
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "select", Type: "function", Function: schema.FunctionCall{Name: "write_trip", Arguments: string(raw)}}}}, nil
}
func (m *sourceFlowModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}

func TestTravelSourceFlowEino(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	trips := &sourceFlowTrips{now: now}
	ref := pm.TravelExecution{Scope: pm.Scope{UserID: "u", SessionID: "s"}, RunID: "run", ConversationID: "conv", Attempt: 1}
	flow := newSourceFlow(flowConfig{Trips: trips, Execution: ref, OriginalQuery: "安排上海到杭州的单程行程", Now: func() time.Time { return now }})
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, Context: flow.decision, OnNativeTrip: flow.recordTrip})
	if err != nil {
		t.Fatal(err)
	}
	flow.registry = registry
	m := &sourceFlowModel{t: t}
	kernel, err := NewKernel(ctx, KernelConfig{Registry: registry, Model: m, MaxIterations: 8, NativeBeforeModel: flow.beforeModel, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, Handlers: []adk.ChatModelAgentMiddleware{&sourceGateIdentity{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}}})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: kernel}).Query(ctx, "安排上海到杭州的单程行程")
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
			message, err := event.Output.MessageOutput.GetMessage()
			if err == nil && message != nil && message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
				final = message.Content
			}
		}
	}
	if trips.saved == nil || trips.saved.Tasks[0].Train == nil || trips.writes != 2 || trips.queries != 1 || m.calls != 2 || !strings.Contains(final, "G1") {
		t.Fatalf("writes=%d queries=%d models=%d final=%q", trips.writes, trips.queries, m.calls, final)
	}
}
