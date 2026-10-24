package integration

import (
	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"context"
	"encoding/json"
	"errors"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	"agent-platform/backend/internal/model"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
)

func TestTravelSourceRecoveryMemory(t *testing.T) {
	ctx := context.Background()
	f, ref, trips := travelFixture(t)
	prefs := prefsvc.New(f.repo)
	text := "以后优先靠窗"
	missing := "memory_missing"
	cmd := model.TravelMemoryCommand{Operation: "update", Updates: []model.TravelMemoryUpdate{{Op: "add", Text: &text}, {Op: "replace", ID: &missing, Text: &text}}}
	first, err := prefs.Update(ctx, ref, "native-memory", cmd)
	if err != nil || len(first.Applied) != 1 || len(first.Rejected) != 1 {
		t.Fatalf("native partial commit: %+v %v", first, err)
	}
	again, err := prefsvc.New(f.repo).Update(ctx, ref, "native-memory", cmd)
	if err != nil || len(again.Applied) != 1 || again.Applied[0].ID != first.Applied[0].ID {
		t.Fatalf("receipt replay changed identity: %+v %v", again, err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_preference_revisions WHERE source_run_id=$1", ref.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated rows", count, err)
	}
	inferred := "经常出差杭州"
	analysis := model.TravelPreferenceAnalysis{InitialSummaryDone: true, CoveredTripIDs: []string{"actual-plan"}}
	saved, err := prefs.SaveAnalysis(ctx, ref, "native-analysis", []model.TravelMemoryUpdate{{Op: "add", Text: &inferred}}, analysis, []string{ref.RunID})
	if err != nil || len(saved.Applied) != 1 {
		t.Fatal("atomic memory plus marker", saved, err)
	}
	rows, err := prefs.ReadEffective(ctx, ref.Scope, ref.RunID)
	if err != nil || len(rows) != 3 {
		t.Fatal("batch rows", rows, err)
	}
	// Provenance must be checked even when the row's own creation Run is valid.
	if _, err = f.pool.Exec(ctx, "UPDATE travel_preference_revisions SET source_runs=$1 WHERE preference_key<>$2", `["excluded-run"]`, first.Applied[0].ID); err != nil {
		t.Fatal(err)
	}
	rows, err = prefs.ReadEffective(ctx, ref.Scope, ref.RunID)
	if err != nil || len(rows) != 1 || rows[0].Key != first.Applied[0].ID {
		t.Fatal("mixed inferred provenance leaked", rows, err)
	}
	p, err := trips.Apply(ctx, ref, "source-plan", travelCommand())
	if err != nil {
		t.Fatal(err)
	}
	history, err := trips.ReadHistory(ctx, ref, model.TravelHistoryRequest{Resource: "trip_summaries"})
	if err != nil || !strings.Contains(string(history), p.Plan.ID) {
		t.Fatal("authorized history", string(history), err)
	}
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: trips, Execution: ref}, History: trips})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"request_kind": "planning", "resource": "trip", "trip_plan_id": p.Plan.ID})
	batch, err := registry.Preflight(ctx, traveltools.ProfileRequest{Role: "main"}, []traveltools.NativeCall{{ID: "read-source", Name: "read", Arguments: args}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := registry.Execute(traveltools.WithOperationID(ctx, "read-source"), batch.Calls[0])
	var observation struct {
		Data struct {
			ID string `json:"trip_plan_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(view), &observation)
	if err != nil || observation.Data.ID != p.Plan.ID {
		t.Fatal("native read exposed Go storage snapshot instead of source contract", view, err)
	}
	for _, resource := range []string{"session_summaries", "session"} {
		data, err := trips.ReadHistory(ctx, ref, model.TravelHistoryRequest{Resource: resource, ConversationID: ref.ConversationID})
		if err != nil || len(data) == 0 {
			t.Fatal("source session read", resource, err)
		}
	}
	// Raw display remains available, Agent history/inspection cannot reimport an excluded Run.
	if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET context_eligible=false WHERE id=$1", ref.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = trips.Inspect(ctx, ref, p.Plan.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("excluded source plan inspected", err)
	}
	if _, err = f.repo.GetTrip(ctx, ref.Scope, p.Plan.ID); err != nil {
		t.Fatal("display history lost", err)
	}
	history, err = trips.ReadHistory(ctx, ref, model.TravelHistoryRequest{Resource: "trip_summaries"})
	if err != nil {
		t.Fatal(err)
	}
	var data any
	if json.Unmarshal(history, &data) != nil || strings.Contains(string(history), p.Plan.ID) {
		t.Fatal("excluded plan discovered", string(history))
	}
}

type sourceBatchModel struct{ calls atomic.Int32 }

type sourceCaptureModel struct{ input string }

func (m *sourceCaptureModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}
func (m *sourceCaptureModel) Generate(_ context.Context, in []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	raw, _ := json.Marshal(in)
	m.input = string(raw)
	return schema.AssistantMessage("已收到当前任务。", nil), nil
}
func (m *sourceCaptureModel) Stream(ctx context.Context, in []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}

func (m *sourceBatchModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}
func (m *sourceBatchModel) Generate(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.calls.Add(1)
	for _, msg := range input {
		if msg.Role == schema.User && strings.Contains(msg.Content, `"tripplans"`) {
			return schema.AssistantMessage("已保留两个草稿，尚未选择交通。", nil), nil
		}
	}
	out := toolDecision("plan-a", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"propose","default_round_trip":false,"tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-10-25"},"items":[{"domain":"train"}]}]}}`)
	out.ToolCalls = append(out.ToolCalls, toolDecision("plan-b", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"propose","default_round_trip":false,"tasks":[{"origin":"南京","destination":"苏州","conditions":{"departure_date":"2026-10-25"},"items":[{"domain":"train"}]}]}}`).ToolCalls...)
	return out, nil
}
func (m *sourceBatchModel) Stream(ctx context.Context, in []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}

type commitGapTrips struct {
	*tripsvc.Service
	lost atomic.Bool
}

func (s *commitGapTrips) Apply(ctx context.Context, ref model.TravelExecution, id string, cmd model.TripCommand) (model.TripResult, error) {
	result, err := s.Service.Apply(ctx, ref, id, cmd)
	if err == nil && s.lost.CompareAndSwap(false, true) {
		return model.TripResult{}, errors.New("injected loss after committed first plan, before observation/checkpoint")
	}
	return result, err
}
func TestTravelSourceRecoveryBatch(t *testing.T) {
	ctx := context.Background()
	f, ref, _ := travelFixture(t)
	provider := &travelQueryProbe{}
	base := tripsvc.NewWithQueries(f.repo, provider, time.Now)
	m := &sourceBatchModel{}
	cfg := travelagent.Config{Model: m, Trips: &commitGapTrips{Service: base}, Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "source-batch-recovery"}
	a, err := travelagent.NewAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	in := contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{RunID: ref.RunID, Role: "user", Content: "保存两个独立单程行程"}}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: time.Second, PauseTimeout: time.Second}}
	exec, err := a.NewExecution(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, err = exec.Start(firstCtx, nil)
	cancel()
	if err == nil {
		t.Fatal("injected process loss did not interrupt")
	}
	var plans int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plans").Scan(&plans); err != nil || plans != 1 {
		t.Fatal("second plan crossed failed first observation", plans, err)
	}
	cfg.Trips = tripsvc.NewWithQueries(f.repo, provider, time.Now)
	freshModel := &sourceBatchModel{}
	cfg.Model = freshModel
	if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET attempt=attempt+1 WHERE id=$1", ref.RunID); err != nil {
		t.Fatal(err)
	}
	in.Run.Attempt++
	fresh, err := travelagent.NewAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	exec, err = fresh.NewExecution(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	result, err := exec.Start(ctx, nil)
	if err != nil || result.FinalText == "" {
		t.Fatal("partial batch did not resume", result, err)
	}
	var receipts, jobs int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM travel_plans),(SELECT count(*) FROM travel_operation_receipts),(SELECT count(*) FROM travel_query_jobs WHERE status='succeeded')`).Scan(&plans, &receipts, &jobs); err != nil || plans != 2 || receipts != 2 || jobs != 2 || provider.calls.Load() != 2 || freshModel.calls.Load() != 1 {
		t.Fatal("partial batch replay duplicated or lost effects", plans, receipts, jobs, provider.calls.Load(), freshModel.calls.Load(), err)
	}
}

func TestTravelSourceRecoveryJobs(t *testing.T) {
	ctx := context.Background()
	f, ref, _ := travelFixture(t)
	provider := &travelQueryProbe{}
	svc := tripsvc.NewWithQueries(f.repo, provider, time.Now)
	cmd := travelCommand()
	cmd.Context = &model.TripDecisionContext{Role: "main", OriginalQuery: "单程差旅"}
	cmd.Tasks[0].Conditions = model.TripConditions{"departure_date": json.RawMessage(`"2026-10-25"`)}
	cmd.Tasks[0].Items = []model.TripQueryItem{{Domain: "train"}}
	p, err := svc.Apply(ctx, ref, "job-plan", cmd)
	if err != nil || len(p.Queries) != 1 {
		t.Fatal("source proposal", p, err)
	}
	jobs, err := traveltools.PlanQueryJobs("job-plan", "call", p)
	if err != nil {
		t.Fatal(err)
	}
	var stored int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_query_jobs WHERE run_id=$1", ref.RunID).Scan(&stored); err != nil || stored != 1 {
		t.Fatal("proposal and jobs not atomic", stored, err)
	}
	e, err := svc.ExecuteQueryJob(ctx, ref, jobs[0].ID, jobs[0].Query)
	if err != nil || e.ID == "" {
		t.Fatal("job result", e, err)
	}
	// Discard every in-memory object before callback/checkpoint. Only Repository remains.
	fresh := tripsvc.NewWithQueries(f.repo, provider, time.Now)
	again, err := fresh.ExecuteQueryJob(ctx, ref, jobs[0].ID, jobs[0].Query)
	if err != nil || again.ID != e.ID || provider.calls.Load() != 1 {
		t.Fatal("paid query repeated after lost checkpoint", again, err, provider.calls.Load())
	}
	current, err := fresh.Inspect(ctx, ref, p.Plan.ID)
	if err != nil || current.Plan.Revision <= p.Plan.Revision || len(current.Plan.QueryResults) != 1 {
		t.Fatal("query business observation not saved", current, err)
	}
	if len(current.Queries) != 0 {
		t.Fatal("inspection invented pending requests despite current persisted evidence", current.Queries)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE travel_query_jobs SET status='running',result=NULL WHERE id=$1", jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET attempt=attempt+1 WHERE id=$1", ref.RunID); err != nil {
		t.Fatal(err)
	}
	old := ref
	ref.Attempt++
	if _, err = fresh.ExecuteQueryJob(ctx, ref, jobs[0].ID, jobs[0].Query); !errors.Is(err, model.ErrTravelOutcomeUnknown) || provider.calls.Load() != 1 {
		t.Fatal("unknown query automatically replayed", err)
	}
	if _, err = fresh.ExecuteQueryJob(ctx, old, jobs[0].ID, jobs[0].Query); !errors.Is(err, model.ErrConflict) {
		t.Fatal("old attempt admitted", err)
	}
}
