package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/model"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
)

func TestTravelMemoryIsolation(t *testing.T) {
	ctx := context.Background()
	f, first, _ := travelFixture(t)
	prefs := prefsvc.New(f.repo)
	command := model.TravelPreferenceCommand{Operation: "set", Key: "seat", Value: "EARLIER_VALID_PREFERENCE", Origin: "explicit"}
	initial, err := prefs.Apply(ctx, first, "pref-first", command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := prefs.Apply(ctx, first, "pref-first", command)
	if err != nil || replayed.Revision != initial.Revision {
		t.Fatalf("preference replay: %+v %v", replayed, err)
	}
	if err = f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: first.Scope, RunID: first.RunID, Attempt: first.Attempt, FinalText: "已完成"}); err != nil {
		t.Fatal(err)
	}
	secondRun, err := f.repo.AcceptRun(ctx, f.input("second", "REPLACED_USER_MESSAGE"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: secondRun.ID, Attempt: 1, From: "accepted", To: "running"}); err != nil {
		t.Fatal(err)
	}
	second := model.TravelExecution{Scope: f.scope, ConversationID: f.conversation, RunID: secondRun.ID, Attempt: 1}
	command.Value = "REPLACED_PREFERENCE"
	if _, err = prefs.Apply(ctx, second, "pref-second", command); err != nil {
		t.Fatal(err)
	}
	summary, err := f.repo.SaveTravelSummary(ctx, second, model.TravelSummary{SourceRuns: []string{first.RunID, second.RunID}, Version: 1, Content: "REPLACED_MIXED_SUMMARY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET status='paused' WHERE id=$1", second.RunID); err != nil {
		t.Fatal(err)
	}
	newRun, err := f.repo.SupersedePausedRun(ctx, f.input("third", "开始新的差旅任务"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: newRun.ID, Attempt: 1, From: "accepted", To: "running"}); err != nil {
		t.Fatal(err)
	}
	current := model.TravelExecution{Scope: f.scope, ConversationID: f.conversation, RunID: newRun.ID, Attempt: 1}
	// Even a summary whose creation run remains valid must validate every source.
	if _, err = f.pool.Exec(ctx, "UPDATE travel_memory_summaries SET source_run_id=$2 WHERE id=$1", summary.ID, first.RunID); err != nil {
		t.Fatal(err)
	}
	messages, err := memory.New(f.repo, 32000).Build(ctx, f.scope, f.conversation, newRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	builder := memory.NewTravelContext(prefs, f.repo, 32000)
	input, err := builder.Build(ctx, f.scope, f.conversation, newRun.ID, messages)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(input)
	if strings.Contains(string(data), "REPLACED_") || !strings.Contains(string(data), "EARLIER_VALID_PREFERENCE") {
		t.Fatalf("memory exclusion/fallback failed: %s", data)
	}
	command.Origin = "inferred"
	command.Value = "INFERRED_OVERRIDE"
	if _, err = prefs.Apply(ctx, current, "inferred", command); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("inference overrode explicit preference: %v", err)
	}
	command.Operation = "retract"
	command.Origin = "explicit"
	command.Value = ""
	if _, err = prefs.Apply(ctx, current, "retract", command); err != nil {
		t.Fatal(err)
	}
	command.Operation = "set"
	command.Origin = "inferred"
	command.Value = "REVIVED_VALUE"
	if _, err = prefs.Apply(ctx, current, "revive", command); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("inference revived retraction: %v", err)
	}
	input, err = builder.Build(ctx, f.scope, f.conversation, newRun.ID, messages)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(input)
	if strings.Contains(string(data), "EARLIER_VALID_PREFERENCE") || strings.Contains(string(data), "REPLACED_") {
		t.Fatalf("retracted or excluded data leaked: %s", data)
	}
	// Low-frequency analysis consumes the same authorized repository history.
	for i := 0; i < 6; i++ {
		if err = f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: current.RunID, Attempt: 1, FinalText: "历史任务已完成"}); err != nil {
			t.Fatal(err)
		}
		r, err := f.repo.AcceptRun(ctx, f.input(fmt.Sprintf("history-%d", i), "我习惯坐二等座"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, From: "accepted", To: "running"}); err != nil {
			t.Fatal(err)
		}
		current.RunID = r.ID
	}
	allowed, err := f.repo.ListContext(ctx, f.scope, f.conversation, current.RunID)
	if err != nil {
		t.Fatal(err)
	}
	maintenance := memory.NewMaintenance(f.repo, prefs)
	if err = maintenance.Maintain(ctx, current, allowed); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Maintain(ctx, current, allowed); err != nil {
		t.Fatal(err)
	}
	input, err = builder.Build(ctx, f.scope, f.conversation, current.RunID, allowed)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(input)
	if strings.Contains(string(data), "REPLACED_") || !strings.Contains(string(data), "二等座") {
		t.Fatalf("analysis bypassed source isolation: %s", data)
	}
	var summaryCount int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM travel_memory_summaries WHERE source_run_id=$1", current.RunID).Scan(&summaryCount); err != nil || summaryCount != 1 {
		t.Fatalf("unchanged history repeated summary: %d %v", summaryCount, err)
	}
	// Verify the actual source-Agent model input, not only a context helper.
	capture := &sourceCaptureModel{}
	agent, err := travelagent.NewAgent(travelagent.Config{Model: capture, Trips: tripsvc.New(f.repo, nil), Preferences: prefs, Context: builder, Decisions: f.repo, Checkpoints: f.repo, Identity: "source-isolation"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.GetRun(ctx, f.scope, current.RunID)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := agent.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: allowed, Limits: contracts.Limits{Iterations: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = execution.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if capture.input == "" || strings.Contains(capture.input, "REPLACED_") || strings.Contains(capture.input, "EARLIER_VALID_PREFERENCE") {
		t.Fatal("actual source model saw excluded/retracted data", capture.input)
	}
}
