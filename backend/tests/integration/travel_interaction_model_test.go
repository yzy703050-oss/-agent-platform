package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

type interactionCase struct {
	name, prompt, memory             string
	tasks, hotels, repeats           int
	sparse, lookup, replan, hotelGap bool
}
type interactionSample struct {
	perfSample
	Audit interactionAudit `json:"interaction_audit"`
}
type interactionReport struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Source      string              `json:"provider_source"`
	Limitations []string            `json:"limitations"`
	Samples     []interactionSample `json:"samples"`
}

type interactionProvider struct {
	inner            *perfProvider
	replan, hotelGap bool
}

func (p *interactionProvider) Search(ctx context.Context, q pm.TravelQuery) (pm.TravelEvidence, error) {
	e, err := p.inner.Search(ctx, q)
	if p.replan && q.Domain == "train" && q.Origin == "上海" && q.Destination == "杭州" {
		e.Candidates = nil
		e.Status = "no_results"
		e.Message = "当前直达查询没有匹配候选。"
	}
	if p.hotelGap && q.Domain == "hotel" {
		for i := range e.Candidates {
			e.Candidates[i].PriceFen = nil
			e.Candidates[i].Remaining = nil
			e.Candidates[i].Availability = "unknown"
		}
	}
	return e, err
}

func TestTravelInteractionModel(t *testing.T) {
	if os.Getenv("REQUIRE_TRAVEL_INTERACTION_MODEL_TEST") != "1" {
		t.Skip("opt-in real model and PostgreSQL interaction acceptance")
	}
	output := os.Getenv("TRAVEL_INTERACTION_OUTPUT")
	if output == "" {
		t.Fatal("TRAVEL_INTERACTION_OUTPUT required to preserve all samples")
	}
	repeats := 3
	if value := os.Getenv("TRAVEL_INTERACTION_REPEATS"); value != "" {
		var err error
		repeats, err = strconv.Atoi(value)
		if err != nil || repeats < 1 || repeats > 3 {
			t.Fatal("repeat count must be 1..3")
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	tomorrow := time.Now().In(loc).AddDate(0, 0, 1)
	d := []string{tomorrow.Format(time.DateOnly), tomorrow.AddDate(0, 0, 1).Format(time.DateOnly), tomorrow.AddDate(0, 0, 2).Format(time.DateOnly)}
	cases := []interactionCase{
		{name: "sparse", prompt: "帮我规划明天去上海的旅途", sparse: true, repeats: repeats},
		{name: "memory_defaults", prompt: "帮我规划明天去杭州的旅途，后天返回。", memory: "我的常住地和出行起点是上海，喜欢高铁二等座，旅游时喜欢自然风景。", tasks: 2, hotels: 1, repeats: repeats},
		{name: "normal_roundtrip", prompt: fmt.Sprintf("请安排%s上海到杭州旅游，%s下午返回上海，住一晚。去程当天下午游览西湖，返程当天上午游览灵隐寺，优先高铁二等座。请选择车次和酒店并保存完整行程。", d[0], d[1]), tasks: 2, hotels: 1, repeats: repeats},
		{name: "multicity", prompt: fmt.Sprintf("安排三段旅游：%s上海到杭州，%s杭州到南京，%s南京返回上海。杭州%s入住%s退房，南京%s入住%s退房，各一晚。首日14:00西湖游览，次日14:00南京博物院游览，优先高铁二等座，选择三段车次及两处酒店并保存完整计划。", d[0], d[1], d[2], d[0], d[1], d[1], d[2]), tasks: 3, hotels: 2, repeats: repeats},
		{name: "query", prompt: fmt.Sprintf("只查询%s上海到杭州的高铁及杭州当天天气，不创建行程。", d[0]), lookup: true, repeats: repeats},
		{name: "policy", prompt: fmt.Sprintf("安排%s上海到杭州单程出差，不返程不住宿，不安排活动，优先高铁二等座，每段硬预算500元。查询适用企业差旅政策，选择车次并保存。", d[0]), tasks: 1, repeats: 1},
		{name: "replan", prompt: fmt.Sprintf("安排%s上海去杭州旅游，%s返回上海，杭州住一晚。优先直达，如果直达没有候选则改为经南京中转去杭州，保留返程和杭州游玩，出发日期可以调整。选择车次及酒店并保存完整可行安排。", d[0], d[2]), tasks: 3, hotels: 1, replan: true, repeats: 1},
		{name: "hotel_gap", prompt: fmt.Sprintf("安排%s上海到杭州旅游，%s返回上海，在杭州住一晚，酒店每晚硬预算400元，不安排固定活动，选择高铁和酒店并保存可用安排。", d[0], d[1]), tasks: 2, hotels: 1, hotelGap: true, repeats: 1},
	}
	report := interactionReport{GeneratedAt: time.Now().UTC(), Source: "real model + real PostgreSQL, fixed supplier/policy fixtures (not disclosed to model)", Limitations: []string{"Does not prove live supplier availability or RAG online quality.", "Small repeated sample; reports observed two-call proportion, not a strict universal bound.", "Policy, maintenance, compaction and retries remain in total actual calls; policy is separate from normal planning target.", "Hotel fixture provides fixed city stay quote; negative case intentionally omits quote and inventory."}, Samples: []interactionSample{}}
	selected := strings.Split(os.Getenv("TRAVEL_INTERACTION_CASES"), ",")
	for _, c := range cases {
		if selected[0] != "" && !slices.Contains(selected, c.name) {
			continue
		}
		for repeat := 1; repeat <= c.repeats; repeat++ {
			t.Run(fmt.Sprintf("%s/%d", c.name, repeat), func(t *testing.T) {
				s := runInteractionModelCase(t, c, repeat)
				report.Samples = append(report.Samples, s)
				data, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(output, []byte(perfRedact(string(data))), 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("INTERACTION case=%s repeat=%d success=%v all_calls=%d planning=%d policy=%d reads=%d duplicate_selects=%d time_ms=%.1f failures=%v", c.name, repeat, s.Success, s.ModelCalls, s.Audit.PlanningCalls, s.Audit.PolicyCalls, s.Audit.HistoryReads, s.Audit.DuplicateSelections, s.DurationMS, s.FailureReasons)
				if !s.Success {
					t.Error("interaction acceptance failed; full evidence retained in output report")
				}
			})
		}
	}
}

func runInteractionModelCase(t *testing.T, c interactionCase, repeat int) interactionSample {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	f, ref, _ := travelFixture(t)
	provider := newPerfProvider(time.Now)
	client := &interactionProvider{inner: provider, replan: c.replan, hotelGap: c.hotelGap}
	service := trip.NewWithQueries(f.repo, client, time.Now).WithQueryNamespace("interaction-acceptance")
	preferences := prefs.New(f.repo)
	if c.memory != "" {
		if _, err := preferences.Update(ctx, ref, "seed-effective-memory", pm.TravelMemoryCommand{Operation: "update", Updates: []pm.TravelMemoryUpdate{{Op: "add", Text: &c.memory}}}); err != nil {
			t.Fatal(err)
		}
	}
	inner, err := llm.NewTravel(ctx, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_API_KEY"))
	if err != nil {
		t.Fatal("model configuration unavailable")
	}
	inputs := &perfInputLog{}
	a, err := travelagent.NewAgent(travelagent.Config{Model: &perfRecordingModel{inner: inner, log: inputs}, Trips: service, Preferences: preferences, Retriever: perfPolicy{}, Decisions: f.repo, Checkpoints: f.repo, Identity: "travel-interaction-acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	s := interactionSample{perfSample: perfSample{Case: c.name, Repeat: repeat, Prompt: c.prompt, Fingerprint: a.Fingerprint(), ToolCalls: map[string]int{}, StageCalls: map[string]int{}, Plans: []pm.TripPlan{}}}
	run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	started := time.Now()
	execution, err := a.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: []pm.Message{{RunID: run.ID, Role: "user", Content: c.prompt}}, Limits: contracts.Limits{Iterations: 12, ToolTimeout: 5 * time.Second, PauseTimeout: 15 * time.Second}})
	if err == nil {
		var result contracts.ExecutionResult
		result, err = execution.Start(ctx, func(e contracts.Event) error {
			mu.Lock()
			defer mu.Unlock()
			s.Events = append(s.Events, e)
			if s.FirstEventMS == nil {
				v := float64(time.Since(started).Microseconds()) / 1000
				s.FirstEventMS = &v
			}
			switch e.Type {
			case "travel.model_call":
				var row travelagent.ModelCallMeasurement
				if json.Unmarshal(e.Payload, &row) == nil {
					s.Calls = append(s.Calls, row)
				}
			case "tool.started":
				var p struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(e.Payload, &p)
				s.ToolCalls[p.Name]++
			case "tool.failed":
				s.ToolFailures++
			}
			return nil
		})
		s.FinalText = result.FinalText
	}
	s.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	if err == nil {
		err = f.repo.CompleteRun(ctx, pm.FinishRunInput{Scope: f.scope, RunID: run.ID, Attempt: run.Attempt, FinalText: s.FinalText})
	}
	if err != nil {
		s.Error = perfRedact(err.Error())
	}
	inputs.mu.Lock()
	s.ModelInputs = append([]perfModelInput(nil), inputs.rows...)
	inputs.mu.Unlock()
	s.ModelCalls = len(s.Calls)
	s.UsageComplete = true
	for _, call := range s.Calls {
		s.StageCalls[call.Stage]++
		s.ModelMS += call.DurationMS
		if call.Usage == nil {
			s.UsageComplete = false
			s.MissingUsageCalls++
			continue
		}
		s.InputTokens += call.Usage.PromptTokens
		s.OutputTokens += call.Usage.CompletionTokens
		s.TotalTokens += call.Usage.TotalTokens
	}
	provider.mu.Lock()
	s.ProviderCalls = provider.Calls
	provider.mu.Unlock()
	auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer auditCancel()
	rows, readErr := f.pool.Query(auditCtx, "SELECT id FROM travel_plans ORDER BY id")
	if readErr != nil {
		t.Fatal(readErr)
	}
	ids := []string{}
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
			t.Fatal("plan read failed")
		}
		s.Plans = append(s.Plans, p)
	}
	if f.pool.QueryRow(auditCtx, "SELECT count(*) FROM travel_plan_revisions").Scan(&s.Revisions) != nil {
		t.Fatal("revision audit failed")
	}
	s.Audit = auditInteraction(s.perfSample)
	auditPerfEvents(&s.perfSample)
	fail := func(reason string) { s.FailureReasons = append(s.FailureReasons, reason) }
	if s.Error != "" {
		fail("execution_failed")
	}
	if strings.TrimSpace(s.FinalText) == "" {
		fail("no_final_reply")
	}
	if s.Audit.Followup {
		fail("asked_user_for_information")
	}
	if s.Audit.HistoryReads > 0 || !c.replan && s.Audit.PlanReads > 0 {
		fail("actively_read_history_for_new_request")
	}
	if s.Audit.DuplicateSelections > 0 {
		fail("duplicate_selection")
	}
	if s.Audit.UnexplainedExtraCalls > 0 {
		fail("unexplained_model_calls")
	}
	if s.ModelCalls != len(s.ModelInputs) {
		fail("actual_model_call_input_count_mismatch")
	}
	if leaksPerfProvenance(s.FinalText) || leaksPerfProvenance(s.ModelInputs) {
		fail("fixture_disclosure")
	}
	if strings.Contains(s.FinalText, "航班") || strings.Contains(s.FinalText, "飞机") {
		fail("unsupported_flight_capability")
	}
	if c.sparse {
		if !strings.Contains(s.FinalText, "上海") {
			fail("missing_destination_suggestions")
		}
		for _, p := range s.Plans {
			for _, task := range p.Tasks {
				if task.Origin != "" {
					fail("invented_origin")
				}
				if task.Train != nil {
					fail("invented_origin_transport")
				}
			}
		}
		if !slices.ContainsFunc([]string{"外滩", "豫园", "陆家嘴", "南京路", "上海博物馆", "武康路"}, func(place string) bool { return strings.Contains(s.FinalText, place) }) {
			fail("no_useful_destination_suggestions")
		}
	} else if c.lookup {
		if len(s.Plans) != 0 || provider.Calls["train"] != 1 || provider.Calls["weather"] != 1 {
			fail("independent_query_scope_changed")
		}
	} else {
		if len(s.Plans) != 1 {
			fail("plan_not_saved_once")
		} else {
			p := s.Plans[0]
			if len(p.Tasks) != c.tasks {
				fail("route_incomplete")
			}
			hotels := 0
			for _, task := range p.Tasks {
				if task.Train == nil {
					fail("train_not_selected")
				}
				if task.Hotel != nil {
					hotels++
				}
			}
			for i := 0; i+1 < len(p.Tasks); i++ {
				stay, onward := p.Tasks[i], p.Tasks[i+1]
				if stay.RequiresHotel && stay.Destination == onward.Origin && stay.CheckOut != "" && onward.DepartureDate > stay.CheckOut {
					fail("uncovered_stay_night")
				}
			}
			if hotels != c.hotels {
				fail("hotel_selection_incomplete")
			}
			if !c.hotelGap && p.Validation.Status != "complete" {
				fail("plan_not_complete")
			}
			if c.hotelGap && p.Validation.Status == "complete" {
				fail("hotel_gap_falsely_complete")
			}
			if c.replan && !slices.ContainsFunc(p.Tasks, func(task pm.TripTask) bool { return task.Destination == "杭州" && len(task.Activities) > 0 }) {
				fail("replan_dropped_destination_activities")
			}
			if c.replan {
				fixedReturn := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).AddDate(0, 0, 3).Format(time.DateOnly)
				if len(p.Tasks) > 0 && p.Tasks[len(p.Tasks)-1].DepartureDate != fixedReturn {
					fail("replan_changed_fixed_return_date")
				}
				for _, task := range p.Tasks {
					if task.RequiresHotel && task.Nights != 1 {
						fail("replan_changed_hard_stay_nights")
					}
				}
			}
			expected := []string{"上海", "杭州", "上海"}
			if c.name == "multicity" {
				expected = []string{"上海", "杭州", "南京", "上海"}
			}
			if c.name == "policy" {
				expected = []string{"上海", "杭州"}
			}
			if c.replan {
				expected = []string{"上海", "南京", "杭州", "上海"}
			}
			for i, task := range p.Tasks {
				if i+1 >= len(expected) || task.Origin != expected[i] || task.Destination != expected[i+1] {
					fail("requested_route_changed")
				}
				if c.name == "policy" && task.Purpose != "business" {
					fail("business_intent_changed")
				}
			}
			if !c.replan {
				tomorrow := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).AddDate(0, 0, 1)
				for i, task := range p.Tasks {
					if task.DepartureDate != tomorrow.AddDate(0, 0, i).Format(time.DateOnly) {
						fail("requested_departure_date_changed")
					}
				}
				if c.name == "normal_roundtrip" && (!hasItineraryActivity(p, "杭州", "西湖", tomorrow.Format(time.DateOnly), "下午") || !hasItineraryActivity(p, "杭州", "灵隐寺", tomorrow.AddDate(0, 0, 1).Format(time.DateOnly), "上午")) {
					fail("requested_activities_not_preserved")
				}
				if c.name == "multicity" && (!hasItineraryActivity(p, "杭州", "西湖", tomorrow.Format(time.DateOnly), "14:00") || !hasItineraryActivity(p, "南京", "博物院", tomorrow.AddDate(0, 0, 1).Format(time.DateOnly), "14:00")) {
					fail("requested_activities_not_preserved")
				}
			}
		}
	}
	if c.memory != "" {
		for _, input := range s.ModelInputs {
			if len(input.Messages) > 0 && input.Messages[0].Role == "system" {
				raw, _ := json.Marshal(input.Messages)
				if !strings.Contains(string(raw), c.memory) {
					fail("injected_memory_missing_from_role")
				}
			}
		}
	}
	if c.name == "policy" && s.Audit.PolicyCalls == 0 {
		fail("policy_precondition_skipped")
	}
	if c.replan && s.Audit.ExtraReasons["replan"] == 0 {
		fail("global_replan_not_observed")
	}
	s.SemanticSuccess = len(s.FailureReasons) == 0
	s.EfficiencySuccess = s.Audit.HistoryReads == 0 && s.Audit.DuplicateSelections == 0 && s.Audit.UnexplainedExtraCalls == 0
	s.Success = s.SemanticSuccess && s.EfficiencySuccess
	return s
}

func hasItineraryActivity(p pm.TripPlan, city, keyword, date, clock string) bool {
	for _, task := range p.Tasks {
		if task.Destination != city {
			continue
		}
		for _, a := range task.Activities {
			if strings.Contains(a.Purpose+a.Description, keyword) && a.Date == date && a.Time == clock {
				return true
			}
		}
	}
	return false
}
