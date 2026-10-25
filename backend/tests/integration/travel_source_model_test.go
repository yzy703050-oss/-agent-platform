package integration

// Opt-in source-fidelity acceptance: real model, fixed mock providers, real PostgreSQL. It reuses this project's real PostgreSQL
// fixture and production Agent/Service; only external tool providers are fixtures.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
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

type perfSample struct {
	SemanticSuccess      bool                               `json:"semantic_success"`
	EfficiencySuccess    bool                               `json:"efficiency_success"`
	FailureReasons       []string                           `json:"failure_reasons,omitempty"`
	RejectedBatches      int                                `json:"rejected_batches"`
	RootRejections       int                                `json:"root_rejections"`
	CollateralRejections int                                `json:"collateral_rejections"`
	DuplicateSelections  int                                `json:"duplicate_selections"`
	ExpectedDates        []string                           `json:"expected_dates"`
	SeedTrainRef         *pm.TravelCandidateRef             `json:"seed_train_ref,omitempty"`
	ModelInputs          []perfModelInput                   `json:"model_inputs"`
	Fingerprint          string                             `json:"workflow_fingerprint"`
	Events               []contracts.Event                  `json:"events"`
	StageCalls           map[string]int                     `json:"stage_calls"`
	Case                 string                             `json:"case"`
	Repeat               int                                `json:"repeat"`
	Prompt               string                             `json:"prompt"`
	Success              bool                               `json:"task_success"`
	Error                string                             `json:"error,omitempty"`
	DurationMS           float64                            `json:"execution_ms"`
	FirstEventMS         *float64                           `json:"first_event_ms"`
	ModelMS              float64                            `json:"model_ms"`
	ModelCalls           int                                `json:"model_calls"`
	UsageComplete        bool                               `json:"usage_complete"`
	MissingUsageCalls    int                                `json:"missing_usage_calls"`
	InputTokens          int                                `json:"input_tokens"`
	OutputTokens         int                                `json:"output_tokens"`
	TotalTokens          int                                `json:"total_tokens"`
	ToolCalls            map[string]int                     `json:"tool_calls"`
	ToolFailures         int                                `json:"tool_failures"`
	ProviderCalls        map[string]int                     `json:"provider_calls"`
	Plans                []pm.TripPlan                      `json:"plans"`
	Revisions            int                                `json:"revision_rows"`
	SeedRevisions        int                                `json:"seed_revision_rows"`
	FinalText            string                             `json:"final_text"`
	Calls                []travelagent.ModelCallMeasurement `json:"calls"`
}

func TestTravelSourceModel(t *testing.T) {
	if os.Getenv("REQUIRE_TRAVEL_SOURCE_MODEL_TEST") != "1" {
		t.Skip("opt-in real model source-fidelity acceptance")
	}
	if os.Getenv("TRAVEL_SOURCE_OUTPUT") == "" {
		t.Fatal("TRAVEL_SOURCE_OUTPUT is required to retain every measurement")
	}
	repeats := 3
	if value := os.Getenv("TRAVEL_SOURCE_REPEATS"); value != "" {
		var err error
		repeats, err = strconv.Atoi(value)
		if err != nil || repeats < 1 || repeats > 3 {
			t.Fatal("TRAVEL_SOURCE_REPEATS must be 1..3")
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	start := time.Now().In(loc).AddDate(0, 0, 2)
	dates := []string{start.Format(time.DateOnly), start.AddDate(0, 0, 1).Format(time.DateOnly), start.AddDate(0, 0, 2).Format(time.DateOnly)}
	report := struct {
		Generation map[string]any `json:"generation_options"`
		Limits     map[string]int `json:"limits"`
		Time       string         `json:"time"`
		Model      string         `json:"model"`
		Mode       string         `json:"tool_mode"`
		Scope      string         `json:"latency_scope"`
		Notes      []string       `json:"notes"`
		Samples    []perfSample   `json:"samples"`
	}{Generation: map[string]any{"temperature": 0.3, "max_tokens": 8192, "deepseek_thinking": "disabled"}, Limits: map[string]int{"main_iterations": 8, "run_seconds": 100, "tool_io_seconds": 5, "model_attempt_seconds": 30, "extra_network_retries": 2}, Time: time.Now().In(loc).Format(time.RFC3339), Model: os.Getenv("LLM_MODEL"), Mode: "fixed complete fixture providers; real model and PostgreSQL; fixture metadata is report-only", Scope: "NewExecution + Start + final Run persistence; excludes migrations/fixture setup, HTTP/SSE/browser and seeded-plan preparation", Notes: []string{fmt.Sprintf("Serial execution, %d repeat(s) per case; no p95 claim from this small sample.", repeats), "Token usage comes only from provider response Usage; nil is missing, never estimated as zero.", "Model calls count Generate invocations; hidden SDK HTTP retries are not separately counted.", "Travel is non-streaming; first_event_ms is first platform event, not time to first token.", "Case modify_activity is seeded through Service before timing; seed revision count is reported.", "All runs use fresh random schemas and cold application query caches; provider prompt cache is not controlled.", "Normal fixtures include hotel quotes; they do not claim the production AMap places client supplies prices. Missing-price behavior has a separate negative regression."}, Samples: []perfSample{}}
	cases := []struct{ id, prompt string }{
		{"query", fmt.Sprintf("请只查询%s上海到杭州的车次和杭州当天天气，不创建行程。", dates[0])},
		{"oneway", fmt.Sprintf("请安排%s上海到杭州单程出差，不返程、不住宿、无活动要求，交通硬预算500元，优先高铁二等座，无到达时限。安排好后选择一条车次并保存行程。", dates[0])},
		{"multicity", fmt.Sprintf("请安排%s上海到杭州、%s杭州到南京、%s南京返回上海的三段往返差旅。杭州%s入住、%s离店，南京%s入住、%s离店，每地一晚。%s杭州14:00客户会议，%s南京14:00项目交流。优先高铁二等座，每段交通硬预算500元，总参考预算2500元为软偏好。请选择三段车次及两处酒店并保存完整行程，不做预订付款。", dates[0], dates[1], dates[2], dates[0], dates[1], dates[1], dates[2], dates[0], dates[1])},
		{"modify_activity", ""},
	}
	for _, c := range cases {
		for repeat := 1; repeat <= repeats; repeat++ {
			t.Run(fmt.Sprintf("%s/%d", c.id, repeat), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
				defer cancel()
				f, ref, _ := travelFixture(t)
				provider := newPerfProvider(time.Now)
				service := trip.NewWithQueries(f.repo, provider, time.Now).WithQueryNamespace("performance-evaluation")
				sample := perfSample{Case: c.id, Repeat: repeat, Prompt: c.prompt, ToolCalls: map[string]int{}, Plans: []pm.TripPlan{}, ExpectedDates: dates}
				if c.id == "modify_activity" {
					command := travelCommand()
					command.Tasks[0].DepartureDate = dates[0]
					command.Tasks[0].Activities = []pm.TripActivity{{ID: "meeting", Purpose: "客户会议", Date: dates[0], Time: "14:00", Source: "user", Strength: pm.Hard}}
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
					seedRef := saved.Plan.Tasks[0].Train.Ref
					sample.SeedTrainRef = &seedRef
					sample.Prompt = fmt.Sprintf("请先读取已保存行程 %s，只把分段 %s 的客户会议开始时间从14:00改为16:00，日期%s不变，其他条件和已选车次保持不变，不重新查询车次或酒店。请保存修改并说明结果。", saved.Plan.ID, saved.Plan.Tasks[0].ID, dates[0])
					provider.Calls = map[string]int{}
				}
				inner, err := llm.NewTravel(ctx, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_API_KEY"))
				if err != nil {
					t.Fatal("model configuration unavailable")
				}

				inputLog := &perfInputLog{}
				recording := &perfRecordingModel{inner: inner, log: inputLog}
				agent, err := travelagent.NewAgent(travelagent.Config{Model: recording, Trips: service, Preferences: prefs.New(f.repo), Retriever: perfPolicy{}, Decisions: f.repo, Checkpoints: f.repo, Identity: "travel-source-acceptance"})
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
				inputLog.mu.Lock()
				sample.ModelInputs = append([]perfModelInput(nil), inputLog.rows...)
				inputLog.mu.Unlock()
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
				for _, id := range ids {
					p, readErr := service.Read(auditCtx, f.scope, id)
					if readErr != nil {
						t.Fatal("saved plan read failed")
					}
					sample.Plans = append(sample.Plans, p)
				}
				if f.pool.QueryRow(auditCtx, "SELECT count(*) FROM travel_plan_revisions").Scan(&sample.Revisions) != nil {
					t.Fatal("revision audit failed")
				}
				evaluatePerfSample(&sample)
				report.Samples = append(report.Samples, sample)
				data, _ := json.MarshalIndent(report, "", "  ")
				if os.WriteFile(os.Getenv("TRAVEL_SOURCE_OUTPUT"), []byte(perfRedact(string(data))), 0600) != nil {
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
