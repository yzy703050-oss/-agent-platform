package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/client/demo"
	"agent-platform/backend/internal/model"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type workflowModel struct {
	calls      atomic.Int32
	boundary   chan struct{}
	blocked    atomic.Bool
	release    chan struct{}
	queryOnly  bool
	queryBatch bool
	command    *model.TripCommand
	repair     bool
}

func (m *workflowModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}
func (m *workflowModel) Generate(ctx context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.calls.Add(1)
	if m.queryOnly {
		for _, msg := range input {
			if msg.Role == schema.Tool {
				return schema.AssistantMessage("已经付款并预订成功，价格1元", nil), nil
			}
		}
		decision := toolDecision("independent-query", "query_travel", `{"domain":"train","origin":"上海","destination":"杭州","date":"2026-10-25"}`)
		if m.queryBatch {
			decision.ToolCalls = append(decision.ToolCalls, toolDecision("other-query", "query_travel", `{"domain":"train","origin":"南京","destination":"苏州","date":"2026-10-26"}`).ToolCalls...)
			decision.ToolCalls = append(decision.ToolCalls, toolDecision("weather-query", "query_travel", `{"domain":"weather","city":"杭州","date":"2026-10-25"}`).ToolCalls...)
		}
		return decision, nil
	}
	var last struct {
		Trip     model.TripResult       `json:"trip"`
		Evidence []model.TravelEvidence `json:"evidence"`
	}
	for _, msg := range input {
		if msg.Role == schema.Tool {
			_ = json.Unmarshal([]byte(msg.Content), &last)
		}
	}
	if last.Trip.Plan.ID == "" {
		command := travelCommand()
		if m.command != nil {
			command = *m.command
		}
		args, _ := json.Marshal(command)
		return toolDecision("create", "write_trip", string(args)), nil
	}
	if last.Trip.Plan.Revision == 1 {
		if m.boundary != nil && m.blocked.CompareAndSwap(false, true) {
			close(m.boundary)
			select {
			case <-m.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		cmd := model.TripCommand{Operation: "select", PlanID: last.Trip.Plan.ID, ExpectedRevision: 1}
		for _, q := range last.Evidence {
			if q.Query.Domain == "train" && len(q.Candidates) > 0 {
				cmd.Selections = append(cmd.Selections, model.TripSelection{TaskID: q.TaskID, Domain: "train", Ref: model.TravelCandidateRef{QueryID: q.ID, ResultRevision: q.ResultRevision, CandidateID: q.Candidates[0].ID}})
			}
		}
		if len(cmd.Selections) == 0 {
			return schema.AssistantMessage("已经付款", nil), nil
		}
		args, _ := json.Marshal(cmd)
		return toolDecision("select", "write_trip", string(args)), nil
	}
	if last.Trip.Plan.Revision == 2 && m.repair {
		activities := []model.TripActivity{{Purpose: "补充会议", Date: "2026-10-25", Source: "user", Strength: model.Hard}}
		cmd := model.TripCommand{Operation: "amend", PlanID: last.Trip.Plan.ID, ExpectedRevision: 2, Changes: []model.TripTaskChange{{TaskID: "t1", Activities: &activities}}}
		args, _ := json.Marshal(cmd)
		return toolDecision("repair", "write_trip", string(args)), nil
	}
	// The platform must render confirmed facts, not repeat this invented claim.
	return schema.AssistantMessage("已付款并预订成功，价格1元", nil), nil
}

func TestTravelWorkflowRecovery(t *testing.T) {
	for _, limit := range []int{8, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f, ref, _ := travelFixture(t)
			provider := &travelQueryProbe{}
			m := &workflowModel{boundary: make(chan struct{}), release: make(chan struct{})}
			cfg := travelagent.Config{Model: m, Trips: tripsvc.NewWithQueries(f.repo, provider, time.Now), Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "recovery-v1"}
			a, err := travelagent.NewAgent(cfg)
			if err != nil {
				t.Fatal(err)
			}
			run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
			if err != nil {
				t.Fatal(err)
			}
			in := contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{RunID: run.ID, Role: "user", Content: "单程差旅"}}, Limits: contracts.Limits{Iterations: limit, ToolTimeout: 5 * time.Second, PauseTimeout: 3 * time.Second}}
			exec, err := a.NewExecution(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, e := exec.Start(ctx, nil); done <- e }()
			select {
			case <-m.boundary:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err = f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: run.ID, Attempt: 1, From: "running", To: "pausing"}); err != nil {
				t.Fatal(err)
			}
			// Let the in-flight response finish within the graceful deadline.
			release := time.AfterFunc(150*time.Millisecond, func() { close(m.release) })
			defer release.Stop()
			paused, err := exec.Pause(ctx)
			if err != nil || !paused.CheckpointSaved {
				t.Fatalf("pause: %+v %v", paused, err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if _, err = f.repo.FinalizePause(ctx, model.FinalizePauseInput{Scope: f.scope, RunID: run.ID, Attempt: 1}); err != nil {
				t.Fatal(err)
			}
			oldCheckpoint, ok, err := f.repo.GetCheckpoint(ctx, f.scope, run.ID)
			if err != nil || !ok {
				t.Fatalf("checkpoint absent: %v", err)
			}
			freshModel := &workflowModel{}
			cfg.Model = freshModel
			fresh, err := travelagent.NewAgent(cfg)
			if err != nil {
				t.Fatal(err)
			}
			in.Resume = true
			if _, err = fresh.NewExecution(ctx, in); err != nil {
				t.Fatal(err)
			}
			if freshModel.calls.Load() != 0 || provider.calls.Load() != 1 {
				t.Fatal("resume probe executed side effects")
			}
			run, err = f.repo.BeginResume(ctx, f.scope, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			run, err = f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: run.ID, Attempt: run.Attempt, From: "resuming", To: "running"})
			if err != nil {
				t.Fatal(err)
			}
			in.Run = run
			exec, err = fresh.NewExecution(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			result, err := exec.Start(ctx, nil)
			if limit == 2 {
				if !errors.Is(err, adk.ErrExceedMaxIterations) || result.ErrorCode != "iteration_limit" {
					t.Fatalf("budget reset on resume: %+v %v", result, err)
				}
			} else if err != nil || !strings.Contains(result.FinalText, "G1") {
				t.Fatalf("resume failed: %+v %v", result, err)
			}
			var revisions int
			if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plan_revisions").Scan(&revisions); err != nil || revisions != 2 || provider.calls.Load() != 1 {
				t.Fatalf("resume replayed side effects: %d/%d %v", revisions, provider.calls.Load(), err)
			}
			if limit == 8 {
				// Recreate the last durable checkpoint, from before the committed
				// selection. This simulates process loss before a newer checkpoint.
				oldCheckpoint.Attempt = run.Attempt
				if err = f.repo.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: f.scope, Checkpoint: oldCheckpoint}); err != nil {
					t.Fatal(err)
				}
				calls := freshModel.calls.Load()
				exec, err = fresh.NewExecution(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = exec.Start(ctx, nil); err != nil {
					t.Fatal(err)
				}
				if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plan_revisions").Scan(&revisions); err != nil || revisions != 2 || freshModel.calls.Load() != calls {
					t.Fatalf("old checkpoint replay repeated accepted effects: %d %v", revisions, err)
				}
			}
		})
	}
}
func (m *workflowModel) Stream(ctx context.Context, in []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func toolDecision(id, name, args string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}}}
}
func modelTripSelection(id string, q model.TravelEvidence) model.TripCommand {
	return model.TripCommand{Operation: "select", PlanID: id, ExpectedRevision: 1, Selections: []model.TripSelection{{TaskID: "t1", Domain: "train", Ref: model.TravelCandidateRef{QueryID: q.ID, ResultRevision: q.ResultRevision, CandidateID: q.Candidates[0].ID}}}}
}

func TestTravelWorkflow(t *testing.T) {
	ctx := context.Background()
	f, ref, _ := travelFixture(t)
	provider := &travelQueryProbe{}
	trips := tripsvc.NewWithQueries(f.repo, provider, time.Now)
	m := &workflowModel{}
	a, err := travelagent.NewAgent(travelagent.Config{Model: m, Trips: trips, Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Context: memory.NewTravelContext(prefsvc.New(f.repo), f.repo, 32000), Identity: "scripted-workflow-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	in := contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{RunID: run.ID, Role: "user", Content: "上海到杭州单程出差"}}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: time.Second * 5, PauseTimeout: time.Second * 5}}
	exec, err := a.NewExecution(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatal("construction executed the workflow")
	}
	result, err := exec.Start(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.FinalText, "G1") || !strings.Contains(result.FinalText, "100.00") || strings.Contains(result.FinalText, "已付款") {
		t.Fatalf("final facts were not rendered from receipts: %s", result.FinalText)
	}
	var plans, revisions, receipts, decisions int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM travel_plans),(SELECT count(*) FROM travel_plan_revisions),(SELECT count(*) FROM travel_operation_receipts),(SELECT count(*) FROM travel_decisions)`).Scan(&plans, &revisions, &receipts, &decisions); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || revisions != 2 || receipts != 2 || decisions != 3 || provider.calls.Load() != 1 {
		t.Fatalf("unexpected persisted workflow: %d/%d/%d/%d queries=%d", plans, revisions, receipts, decisions, provider.calls.Load())
	}
	// Simulate loss of the checkpoint after committed effects: a fresh execution
	// replays accepted decisions and receipts, without a new model or mutation.
	exec, err = a.NewExecution(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exec.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 3 || provider.calls.Load() != 1 {
		t.Fatal("replay regenerated a decision or repeated a query")
	}
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plan_revisions").Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("replay duplicated revisions: %d %v", revisions, err)
	}
}

func TestTravelIndependentQuery(t *testing.T) {
	ctx := context.Background()
	f, ref, _ := travelFixture(t)
	m := &workflowModel{queryOnly: true, queryBatch: true}
	provider := demo.New(time.Now)
	a, err := travelagent.NewAgent(travelagent.Config{Model: m, Trips: tripsvc.NewWithQueries(f.repo, provider, time.Now), Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "independent-query"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := a.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{Role: "user", Content: "只查车次"}}, Limits: contracts.Limits{Iterations: 4, ToolTimeout: time.Second * 5}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := exec.Start(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.FinalText, "100.00") || strings.Contains(result.FinalText, "已经付款") {
		t.Fatalf("query response fabricated facts: %s", result.FinalText)
	}
	for _, fact := range []string{"上海", "南京", "22.0°C", "演示数据"} {
		if !strings.Contains(result.FinalText, fact) {
			t.Fatalf("batch query lost confirmed fact %q: %s", fact, result.FinalText)
		}
	}
	var plans int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plans").Scan(&plans); err != nil || plans != 0 {
		t.Fatalf("independent query created plan: %d %v", plans, err)
	}
}

func TestTravelWorkflowVariants(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint("partial=", partial), func(t *testing.T) {
			ctx := context.Background()
			f, ref, _ := travelFixture(t)
			cmd := travelCommand()
			round := true
			cmd.RoundTrip = &round
			cmd.Tasks = append(cmd.Tasks, model.TripTask{Origin: "杭州", Destination: "上海", DepartureDate: "2026-10-27"})
			provider := &travelQueryProbe{}
			client := travelQueryFunc(func(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
				if partial {
					provider.calls.Add(1)
					return model.TravelEvidence{Status: "unavailable", Message: "供应方未配置。"}, nil
				}
				return provider.Search(ctx, q)
			})
			m := &workflowModel{command: &cmd, repair: !partial}
			a, err := travelagent.NewAgent(travelagent.Config{Model: m, Trips: tripsvc.NewWithQueries(f.repo, client, time.Now), Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "variants"})
			if err != nil {
				t.Fatal(err)
			}
			run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
			if err != nil {
				t.Fatal(err)
			}
			exec, err := a.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{Role: "user", Content: "往返并补充会议"}}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: 5 * time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := exec.Start(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if partial {
				want = 1
			}
			var revisions int
			if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_plan_revisions").Scan(&revisions); err != nil || revisions != want || provider.calls.Load() != 2 {
				t.Fatalf("revision/query mismatch: %d/%d %v", revisions, provider.calls.Load(), err)
			}
			if strings.Contains(result.FinalText, "已经付款") || !strings.Contains(result.FinalText, "杭州 → 上海") {
				t.Fatal("invalid final facts", result.FinalText)
			}
			if partial && !strings.Contains(result.FinalText, "尚未选择") {
				t.Fatal("missing partial result", result.FinalText)
			}
		})
	}
}
