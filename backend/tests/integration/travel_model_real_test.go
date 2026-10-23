package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/client/hotel"
	"agent-platform/backend/internal/client/llm"
	"agent-platform/backend/internal/client/train"
	"agent-platform/backend/internal/model"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
)

func TestTravelModelReal(t *testing.T) {
	if os.Getenv("REQUIRE_TRAVEL_MODEL_TEST") != "1" {
		t.Skip("opt-in real model workflow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	f, ref, _ := travelFixture(t)
	m, err := llm.New(ctx, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	providers := tripsvc.Providers{"train": train.New(os.Getenv("TRAIN_API_KEY"), nil, time.Now), "hotel": hotel.New(os.Getenv("AMAP_API_KEY"), nil, time.Now)}
	a, err := travelagent.NewAgent(travelagent.Config{Model: m, Trips: tripsvc.NewWithQueries(f.repo, providers, time.Now), Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "real-model-smoke"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	input := contracts.RunInput{Scope: f.scope, Run: run, Messages: []model.Message{{Role: "user", Content: "请保存2026年10月25日上海到杭州单程差旅行程，不住宿不返程，无硬到达时间。查询车次并选择一条真实候选保存。没有政策依据就保持未核验。"}}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: 15 * time.Second, PauseTimeout: 15 * time.Second}}
	exec, err := a.NewExecution(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := exec.Start(ctx, nil)
	if err != nil {
		message := err.Error()
		for _, name := range []string{"LLM_API_KEY", "TRAIN_API_KEY", "AMAP_API_KEY", "DB_DSN", "TEST_DATABASE_URL"} {
			if v := os.Getenv(name); v != "" {
				message = strings.ReplaceAll(message, v, "[redacted]")
			}
		}
		t.Fatal(message)
	}
	if !strings.Contains(result.FinalText, "行程已保存") {
		t.Fatal("model did not exercise persisted trip planning")
	}
	var plans, revisions int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM travel_plans),(SELECT count(*) FROM travel_plan_revisions)`).Scan(&plans, &revisions); err != nil {
		t.Fatal(err)
	}
	t.Logf("real model workflow: plans=%d revisions=%d", plans, revisions)
}
