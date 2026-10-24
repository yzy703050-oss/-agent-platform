package integration

// Opt-in source-fidelity acceptance: real model, fixed mock providers, real PostgreSQL. It reuses this project's real PostgreSQL
// fixture and production Agent/Service; only external tool providers are fixtures.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/client/demo"
	"agent-platform/backend/internal/client/llm"
	pm "agent-platform/backend/internal/model"
	prefs "agent-platform/backend/internal/service/travelpreference"
	trip "agent-platform/backend/internal/service/trip"
)

func perfRedact(s string) string {
	for _, key := range []string{"LLM_API_KEY", "TRAIN_API_KEY", "AMAP_API_KEY", "DB_DSN", "TEST_DATABASE_URL"} {
		if v := os.Getenv(key); v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

type perfProvider struct {
	mu    sync.Mutex
	inner trip.QueryClient
	Calls map[string]int
}

func (p *perfProvider) Search(ctx context.Context, q pm.TravelQuery) (pm.TravelEvidence, error) {
	p.mu.Lock()
	p.Calls[q.Domain]++
	p.mu.Unlock()
	return p.inner.Search(ctx, q)
}

type perfPolicy struct{}

func (perfPolicy) Search(context.Context, string) (pm.PolicyEvidence, error) {
	return pm.PolicyEvidence{IsDemo: true, Status: "ok", Collection: "agent_platform_demo", Fragments: []pm.PolicyFragment{{ID: "demo-policy", Content: "演示政策（虚构）：高铁二等座，单程交通演示额度500元；杭州、南京住宿演示额度400元/晚。未知房价和库存必须保留未核验状态，不能视作0元，不能宣称正式报销合规。", Source: "demo-travel-policy.txt"}}}, nil
}

type perfSample struct {
	Fingerprint       string                             `json:"workflow_fingerprint"`
	Events            []contracts.Event                  `json:"events"`
	StageCalls        map[string]int                     `json:"stage_calls"`
	Case              string                             `json:"case"`
	Repeat            int                                `json:"repeat"`
	Prompt            string                             `json:"prompt"`
	Success           bool                               `json:"task_success"`
	Error             string                             `json:"error,omitempty"`
	DurationMS        float64                            `json:"execution_ms"`
	FirstEventMS      *float64                           `json:"first_event_ms"`
	ModelMS           float64                            `json:"model_ms"`
	ModelCalls        int                                `json:"model_calls"`
	UsageComplete     bool                               `json:"usage_complete"`
	MissingUsageCalls int                                `json:"missing_usage_calls"`
	InputTokens       int                                `json:"input_tokens"`
	OutputTokens      int                                `json:"output_tokens"`
	TotalTokens       int                                `json:"total_tokens"`
	ToolCalls         map[string]int                     `json:"tool_calls"`
	ToolFailures      int                                `json:"tool_failures"`
	ProviderCalls     map[string]int                     `json:"provider_calls"`
	Plans             []pm.TripPlan                      `json:"plans"`
	Revisions         int                                `json:"revision_rows"`
	SeedRevisions     int                                `json:"seed_revision_rows"`
	FinalText         string                             `json:"final_text"`
	Calls             []travelagent.ModelCallMeasurement `json:"calls"`
}

func TestTravelSourceModel(t *testing.T) {
	if os.Getenv("REQUIRE_TRAVEL_SOURCE_MODEL_TEST") != "1" {
		t.Skip("opt-in real model source-fidelity acceptance")
	}
	if os.Getenv("TRAVEL_SOURCE_OUTPUT") == "" {
		t.Fatal("TRAVEL_SOURCE_OUTPUT is required to retain every measurement")
	}
	report := struct {
		Generation map[string]any `json:"generation_options"`
		Limits     map[string]int `json:"limits"`
		Time       string         `json:"time"`
		Model      string         `json:"model"`
		Mode       string         `json:"tool_mode"`
		Scope      string         `json:"latency_scope"`
		Notes      []string       `json:"notes"`
		Samples    []perfSample   `json:"samples"`
	}{Generation: map[string]any{"temperature": 0.3, "max_tokens": 8192, "deepseek_thinking": "disabled"}, Limits: map[string]int{"main_iterations": 8, "run_seconds": 100, "tool_io_seconds": 5, "model_attempt_seconds": 30, "extra_network_retries": 2}, Time: time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339), Model: os.Getenv("LLM_MODEL"), Mode: "fixed demo providers; real model and PostgreSQL", Scope: "NewExecution + Start + final Run persistence; excludes migrations/fixture setup, HTTP/SSE/browser and seeded-plan preparation", Notes: []string{"Serial execution, 3 repeats per case; no p95 claim from this small sample.", "Token usage comes only from provider response Usage; nil is missing, never estimated as zero.", "Model calls count Generate invocations; hidden SDK HTTP retries are not separately counted.", "Travel is non-streaming; first_event_ms is first platform event, not time to first token.", "Case modify_activity is seeded through Service before timing; seed revision count is reported.", "All runs use fresh random schemas and cold application query caches; provider prompt cache is not controlled."}, Samples: []perfSample{}}
	cases := []struct{ id, prompt string }{
		{"query", "请只查询2026-10-26上海到杭州的演示车次和杭州当天天气，不创建行程。展示查询结果，标注演示数据。"},
		{"oneway", "请安排2026-10-26上海到杭州单程出差，不返程、不住宿、无活动要求，交通硬预算500元，优先高铁二等座，无到达时限。保存行程，查询并选择一条可用演示车次持久化。无需检索政策；未知内容保持未核验。"},
		{"multicity", "请安排2026-10-26上海到杭州、10月27日杭州到南京、10月28日南京返回上海的三段往返差旅。杭州入住26日离店27日，南京入住27日离店28日，每地一晚。26日杭州14:00客户会议，27日南京14:00项目交流。优先高铁二等座，每段交通硬预算500元，总参考预算2500元为软偏好。保存完整行程，并查询和选择三段车次及两处酒店地点。演示房价、库存未知，不视作0元，不做预订付款；无需额外政策或网页检索。"},
		{"modify_activity", ""},
	}
	for _, c := range cases {
		for repeat := 1; repeat <= 3; repeat++ {
			t.Run(fmt.Sprintf("%s/%d", c.id, repeat), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
				defer cancel()
				f, ref, _ := travelFixture(t)
				provider := &perfProvider{inner: demo.New(time.Now), Calls: map[string]int{}}
				service := trip.NewWithQueries(f.repo, provider, time.Now).WithQueryNamespace("performance-demo")
				sample := perfSample{Case: c.id, Repeat: repeat, Prompt: c.prompt, ToolCalls: map[string]int{}, Plans: []pm.TripPlan{}}
				if c.id == "modify_activity" {
					command := travelCommand()
					command.Tasks[0].DepartureDate = "2026-10-26"
					command.Tasks[0].Activities = []pm.TripActivity{{ID: "meeting", Purpose: "客户会议", Date: "2026-10-26", Time: "14:00", Source: "user", Strength: pm.Hard}}
					saved, err := service.Apply(ctx, ref, "seed-create", command)
					if err != nil {
						t.Fatal("seed plan failed")
					}
					query := saved.Queries[0]
					e, err := service.Query(ctx, ref, query)
					if err != nil {
						t.Fatal("seed query failed")
					}
					saved, err = service.Apply(ctx, ref, "seed-select", pm.TripCommand{Operation: "select", PlanID: saved.Plan.ID, ExpectedRevision: 1, Selections: []pm.TripSelection{{TaskID: saved.Plan.Tasks[0].ID, Domain: "train", Ref: pm.TravelCandidateRef{QueryID: e.ID, ResultRevision: 1, CandidateID: e.Candidates[0].ID}}}})
					if err != nil {
						t.Fatal("seed selection failed")
					}
					sample.SeedRevisions = 2
					sample.Prompt = fmt.Sprintf("请先读取已保存行程 %s，只把分段 %s 的客户会议开始时间从14:00改为16:00，日期2026-10-26不变，其他条件和已选车次保持不变，不重新查询车次或酒店。请保存修改并说明结果。", saved.Plan.ID, saved.Plan.Tasks[0].ID)
					provider.Calls = map[string]int{}
				}
				inner, err := llm.NewTravel(ctx, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_API_KEY"))
				if err != nil {
					t.Fatal("model configuration unavailable")
				}

				agent, err := travelagent.NewAgent(travelagent.Config{Model: inner, Trips: service, Preferences: prefs.New(f.repo), Retriever: perfPolicy{}, Decisions: f.repo, Checkpoints: f.repo, Identity: "travel-source-acceptance"})
				if err != nil {
					t.Fatal("agent construction failed")
				}
				sample.Fingerprint = agent.Fingerprint()
				run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
				if err != nil {
					t.Fatal("run unavailable")
				}
				var eventMu sync.Mutex
				started := time.Now()
				exec, err := agent.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: []pm.Message{{RunID: run.ID, Role: "user", Content: sample.Prompt}}, Limits: contracts.Limits{Iterations: 8, ToolTimeout: 5 * time.Second, PauseTimeout: 15 * time.Second}})
				if err == nil {
					result, runErr := exec.Start(ctx, func(e contracts.Event) error {
						eventMu.Lock()
						defer eventMu.Unlock()
						sample.Events = append(sample.Events, e)
						if sample.FirstEventMS == nil {
							v := float64(time.Since(started).Microseconds()) / 1000
							sample.FirstEventMS = &v
						}
						if e.Type == "travel.model_call" {
							var row travelagent.ModelCallMeasurement
							if err := json.Unmarshal(e.Payload, &row); err != nil {
								return err
							}
							sample.Calls = append(sample.Calls, row)
						}
						if e.Type == "tool.started" {
							var p struct {
								Name string `json:"name"`
							}
							_ = json.Unmarshal(e.Payload, &p)
							sample.ToolCalls[p.Name]++
						}
						if e.Type == "tool.failed" {
							sample.ToolFailures++
						}
						return nil
					})
					sample.FinalText = result.FinalText
					err = runErr
				}
				if err == nil {
					err = f.repo.CompleteRun(ctx, pm.FinishRunInput{Scope: f.scope, RunID: run.ID, Attempt: run.Attempt, FinalText: sample.FinalText})
				}
				sample.DurationMS = float64(time.Since(started).Microseconds()) / 1000
				if err != nil {
					sample.Error = perfRedact(err.Error())
				}
				sample.Calls = append([]travelagent.ModelCallMeasurement(nil), sample.Calls...)
				sample.ModelCalls = len(sample.Calls)
				sample.UsageComplete = true
				for _, call := range sample.Calls {
					if sample.StageCalls == nil {
						sample.StageCalls = map[string]int{}
					}
					sample.StageCalls[call.Stage]++
					sample.ModelMS += call.DurationMS
					if call.Usage == nil {
						sample.MissingUsageCalls++
						sample.UsageComplete = false
						continue
					}
					sample.InputTokens += call.Usage.PromptTokens
					sample.OutputTokens += call.Usage.CompletionTokens
					sample.TotalTokens += call.Usage.TotalTokens
				}
				provider.mu.Lock()
				sample.ProviderCalls = provider.Calls
				provider.mu.Unlock()
				auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer auditCancel()
				rows, readErr := f.pool.Query(auditCtx, "SELECT id FROM travel_plans ORDER BY id")
				if readErr != nil {
					t.Fatal("plan audit failed")
				}
				var ids []string
				for rows.Next() {
					var id string
					if rows.Scan(&id) != nil {
						t.Fatal("plan scan failed")
					}
					ids = append(ids, id)
				}
				rows.Close()
				tasks, trains, hotels, activities := 0, 0, 0, 0
				modified := false
				for _, id := range ids {
					p, readErr := service.Read(auditCtx, f.scope, id)
					if readErr != nil {
						t.Fatal("saved plan read failed")
					}
					sample.Plans = append(sample.Plans, p)
					tasks += len(p.Tasks)
					for _, task := range p.Tasks {
						if task.Train != nil {
							trains++
						}
						if task.Hotel != nil {
							hotels++
						}
						activities += len(task.Activities)
						for _, a := range task.Activities {
							modified = modified || strings.Contains(a.Time, "16:00")
						}
					}
				}
				if f.pool.QueryRow(auditCtx, "SELECT count(*) FROM travel_plan_revisions").Scan(&sample.Revisions) != nil {
					t.Fatal("revision audit failed")
				}
				sample.Success = err == nil
				switch c.id {
				case "query":
					sample.Success = sample.Success && len(ids) == 0 && provider.Calls["train"] > 0 && provider.Calls["weather"] > 0
				case "oneway":
					sample.Success = sample.Success && tasks == 1 && trains == 1 && sample.Revisions >= 2
				case "multicity":
					sample.Success = sample.Success && len(ids) == 1 && tasks == 3 && trains == 3 && hotels == 2 && activities >= 2
				case "modify_activity":
					sample.Success = sample.Success && len(ids) == 1 && tasks == 1 && trains == 1 && modified && len(provider.Calls) == 0 && sample.Revisions > sample.SeedRevisions
				}
				report.Samples = append(report.Samples, sample)
				data, _ := json.MarshalIndent(report, "", "  ")
				if os.WriteFile(os.Getenv("TRAVEL_SOURCE_OUTPUT"), data, 0600) != nil {
					t.Fatal("cannot save measurement report")
				}
				t.Logf("PERF case=%s repeat=%d success=%v time_ms=%.1f calls=%d input=%d output=%d total=%d usage_complete=%v", c.id, repeat, sample.Success, sample.DurationMS, sample.ModelCalls, sample.InputTokens, sample.OutputTokens, sample.TotalTokens, sample.UsageComplete)
				if !sample.Success {
					t.Errorf("task did not meet scenario outcome; see JSON report")
				}
			})
		}
	}
}
