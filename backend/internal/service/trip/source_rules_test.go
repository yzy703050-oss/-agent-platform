package trip

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

func TestTripSourceRules(t *testing.T) {
	t.Run("inferred_deadline_accepts_source_soft_correction", func(t *testing.T) {
		oneway := false
		now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		for _, source := range []string{"derived", "proposal", "default", "preference"} {
			for _, level := range []model.ConstraintStrength{model.Hard, model.Soft} {
				cmd := model.TripCommand{Operation: "propose", RoundTrip: &oneway, Context: &model.TripDecisionContext{Role: "main"}, Tasks: []model.TripTask{{Origin: "上海", Destination: "杭州", Conditions: sourceConditions(`{"departure_date":"2026-10-26","arrival_before":"2026-10-26T13:00:00+08:00"}`), FieldSources: map[string]string{"arrival_before": source}, ConditionStrengths: map[string]model.ConstraintStrength{"arrival_before": level}, Items: []model.TripQueryItem{{Domain: "train"}}}}}
				out, err := Preview(nil, cmd, nil, now)
				if level == model.Hard {
					if err == nil {
						t.Fatal("inferred hard deadline accepted", source)
					}
				} else if err != nil || len(out.Queries) != 1 {
					t.Fatal("source soft correction still rejected", source, err)
				}
			}
		}
	})
	t.Run("native_activity_edit_preserves_legacy_saved_route_and_selection", func(t *testing.T) {
		p := basePlan()
		p.Tasks = p.Tasks[:1]
		p.RoundTrip = false
		p.Tasks[0].Activities = []model.TripActivity{{ID: "meeting", Date: "2026-10-25", Time: "14:00", Purpose: "客户会议", Description: "客户会议", Source: "user", Strength: model.Hard}}
		activities := []model.TripActivity{{ID: "meeting", Date: "2026-10-25", Time: "16:00", Purpose: "客户会议", Description: "客户会议", Source: "user", Strength: model.Hard}}
		result, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main", OriginalQuery: "只把会议从14:00改到16:00"}, Changes: []model.TripTaskChange{{TaskID: "t1", Activities: &activities}}}, nil, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
		if err != nil || result.Plan.Tasks[0].DepartureDate != "2026-10-25" || result.Plan.Tasks[0].Train == nil || result.Plan.Tasks[0].Train.Ref != p.Tasks[0].Train.Ref || result.Plan.Constraints.MaxBudgetFen == nil || *result.Plan.Constraints.MaxBudgetFen != *p.Constraints.MaxBudgetFen || len(result.Queries) != 0 {
			t.Fatal("native edit lost legacy immutable facts", result, err)
		}
	})
	t.Run("expiry_refresh_requires_current_exact_task_binding", func(t *testing.T) {
		now := time.Now()
		e := model.TravelEvidence{ID: "q", ResultRevision: 1, UserID: "u", ConversationID: "c", PlanID: "p", TaskID: "t", Status: "ok", ExpiresAt: now.Add(-time.Minute), Query: model.TravelQuery{Domain: "train", TaskRevision: 2}, Candidates: []model.TravelCandidate{{ID: "G1", Name: "G1"}}}
		p := model.TripPlan{ID: "p", UserID: "u", ConversationID: "c", SourceVersion: 1, Tasks: []model.TripTask{{ID: "t", Revision: 2}}, QueryResults: map[string]model.TravelEvidence{"q": e}}
		s := model.TripSelection{TaskID: "t", Domain: "train", Ref: model.TravelCandidateRef{QueryID: "q", ResultRevision: 1, CandidateID: "G1"}}
		if expiredSelection(&p, []model.TripSelection{s}, now) == nil {
			t.Fatal("valid expired binding not refreshable")
		}
		s.TaskID = "other"
		if expiredSelection(&p, []model.TripSelection{s}, now) != nil {
			t.Fatal("wrong task triggered network refresh")
		}
		s.TaskID = "t"
		s.Domain = "hotel"
		if expiredSelection(&p, []model.TripSelection{s}, now) != nil {
			t.Fatal("wrong domain triggered network refresh")
		}
		s.Domain = "train"
		e.Query.TaskRevision = 1
		p.QueryResults["q"] = e
		if expiredSelection(&p, []model.TripSelection{s}, now) != nil {
			t.Fatal("obsolete task binding triggered refresh")
		}
	})
	t.Run("quote_requirement_uses_only_the_matching_clause", func(t *testing.T) {
		if !sourceUserWantsQuote("查询酒店价格和火车票") || sourceUserWantsQuote("不用查询酒店价格") || sourceUserWantsQuote("查火车票价格，酒店地点即可") {
			t.Fatal("source hotel quote evidence semantics changed")
		}
	})
	t.Run("canonical_train_kind", func(t *testing.T) {
		if !matchesTrain(model.TravelCandidate{Name: "G1", TrainKind: "G"}, model.TripConstraints{TrainKinds: []string{"high_speed"}}) {
			t.Fatal("G train must match original high_speed constraint")
		}
		if matchesTrain(model.TravelCandidate{Name: "D1", TrainKind: "D"}, model.TripConstraints{TrainKinds: []string{"high_speed"}}) {
			t.Fatal("EMU is not a high_speed train")
		}
	})
}

func TestTripBoundReply(t *testing.T) {
	p := basePlan()
	p.RoundTrip = false
	p.Tasks = p.Tasks[:1]
	p.Tasks[0].Activities = []model.TripActivity{{ID: "meeting", Purpose: "会议", Description: "参加会议", Date: "2026-10-25", Time: "16:00", Source: "user", Strength: model.Hard}}
	result := model.TripResult{Operation: "amend", Plan: p, Receipt: &model.TravelReceipt{OperationID: "change", PlanID: p.ID, Revision: p.Revision}}
	result.Validation = model.TripValidation{Status: "partial", Issues: []model.TripIssue{{Code: "hotel_price_unknown", Blocking: true, Message: "房价未知"}}}
	bound, err := BindReply(result, model.TripReplySpec{Tasks: map[string][]string{"t1": {"content"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bound.Text, "16:00") || strings.Contains(bound.Text, "14:00") || strings.Contains(bound.Text, "G1") {
		t.Fatalf("local reply must render saved activity only: %s", bound.Text)
	}
	result.Receipt.Revision--
	if _, err = BindReply(result, model.TripReplySpec{}); err == nil {
		t.Fatal("stale receipt accepted as current save")
	}
	result.Receipt = nil
	if _, err = BindReply(result, model.TripReplySpec{}); err == nil {
		t.Fatal("uncommitted plan accepted as saved")
	}
	result.Receipt = &model.TravelReceipt{OperationID: "change", PlanID: p.ID, Revision: p.Revision}
	if _, err = BindReply(result, model.TripReplySpec{Tasks: map[string][]string{"unknown": {"content"}}}); err == nil {
		t.Fatal("unknown task reply accepted")
	}
	if _, err = BindReply(result, model.TripReplySpec{Explanation: "已安排16点"}); err == nil {
		t.Fatal("prose must not duplicate numerical bound facts")
	}
	p.Tasks[0].Passengers = 2
	amount := int64(10001)
	p.Tasks[0].Train.Candidate.PriceFen = &amount
	result.Plan = p
	bound, err = BindReply(result, model.TripReplySpec{})
	if err != nil || !strings.Contains(bound.Text, "200.02元") {
		t.Fatalf("reply lost passenger amount: %s, %v", bound.Text, err)
	}
	result.Operation = "propose"
	if _, err = BindReply(result, model.TripReplySpec{}); err == nil {
		t.Fatal("proposal cannot submit a final bound reply")
	}
	result.Operation = "amend"
	result.RouteChanged = true
	if _, err = BindReply(result, model.TripReplySpec{Tasks: map[string][]string{"t1": {"content"}}}); err == nil {
		t.Fatal("route changes must show saved full route")
	}
}

func sourceConditions(s string) model.TripConditions {
	var out model.TripConditions
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		panic(err)
	}
	return out
}

func TestTripSourceConditions(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)
	no := false
	cmd := model.TripCommand{Operation: "propose", RoundTrip: &no, Context: &model.TripDecisionContext{Role: "main", OriginalQuery: "10月26日上海到杭州单程"},
		ConfirmedConditions: sourceConditions(`{"passengers":2,"constraints":{"total_budget_cny":500}}`),
		Tasks: []model.TripTask{{Origin: "上海", Destination: "杭州", Purpose: "visit", Conditions: sourceConditions(`{"departure_date":"2026-10-26","constraints":{"train_kind":"high_speed"}}`),
			FieldSources: map[string]string{"departure_date": "user", "train_kind": "proposal"}, ConditionStrengths: map[string]model.ConstraintStrength{"departure_date": model.Hard, "train_kind": model.Soft},
			Items: []model.TripQueryItem{{Domain: "train"}}, Activities: []model.TripActivity{{Purpose: "游览", Description: "抵达后游览", Date: "2026-10-26", Time: "下午", Source: "proposal", Strength: model.Soft}}}},
	}
	result, err := Preview(nil, cmd, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Tasks[0].DepartureDate != "2026-10-26" || result.Plan.Tasks[0].Revision != 1 || result.Plan.Tasks[0].Passengers != 2 || result.Plan.SourceVersion != 1 {
		t.Fatalf("source conditions lost: %+v", result.Plan)
	}
	if len(result.Queries) != 1 || len(result.Queries[0].Constraints.TrainKinds) != 0 {
		t.Fatalf("soft preference became hard query: %+v", result.Queries)
	}
	var snapshot struct {
		Revision int64 `json:"revision"`
	}
	if json.Unmarshal(result.Plan.ConstraintSnapshot, &snapshot) != nil || snapshot.Revision != 1 {
		t.Fatal("constraint snapshot revision missing")
	}
	p := result.Plan
	p.ID = "p"
	p.Tasks[0].Train = &model.TravelSelection{Candidate: model.TravelCandidate{Name: "G1"}}
	p.Tasks[0].Activities = []model.TripActivity{{ID: "meeting", Purpose: "会议", Description: "14:00会议", Date: "2026-10-26", Time: "14:00", Source: "user", Strength: model.Hard}}
	updated := []model.TripActivity{{ID: "meeting", Purpose: "会议", Description: "14:00会议", Date: "2026-10-26", Time: "16:00", Source: "user", Strength: model.Hard}}
	change := model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main", OriginalQuery: "只把会议从14:00改到16:00"}, Changes: []model.TripTaskChange{{TaskID: p.Tasks[0].ID, Activities: &updated, Components: []string{"content"}}}}
	modified, err := Preview(&p, change, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if modified.Plan.Tasks[0].Activities[0].Time != "16:00" || modified.Plan.Tasks[0].Train == nil || len(modified.Queries) != 0 || !strings.Contains(modified.Plan.Tasks[0].Activities[0].Description, "16:00") {
		t.Fatalf("activity edit was not preserved correctly: %+v", modified)
	}
	change.Context.Role = "verifier"
	if _, err = Preview(&p, change, nil, now); err == nil {
		t.Fatal("Verifier changed existing hard activity without replanning")
	}
	for _, tc := range []struct {
		input string
		fen   int64
		bad   bool
	}{
		{"100.01", 10001, false}, {"0", 0, false}, {"-1", 0, true}, {`"100"`, 0, true}, {"0.001", 0, true}, {"92233720368547758.08", 0, true},
	} {
		value, err := MoneyFen(json.RawMessage(tc.input))
		if tc.bad {
			if err == nil {
				t.Fatalf("invalid money accepted: %s", tc.input)
			}
			continue
		}
		if err != nil || value != tc.fen {
			t.Fatalf("inexact money %s => %d, %v", tc.input, value, err)
		}
	}
}

func TestTripSourceBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)
	falseValue := false
	create := model.TripCommand{Operation: "propose", RoundTrip: &falseValue, Context: &model.TripDecisionContext{Role: "main"}, Tasks: []model.TripTask{{Origin: "上海", Destination: "杭州", Purpose: "visit", Conditions: sourceConditions(`{"departure_date":"2026-10-26","check_in":"2026-10-26","check_out":"2026-10-28"}`), FieldSources: map[string]string{"departure_date": "user"}, Items: []model.TripQueryItem{{Domain: "train"}, {Domain: "hotel"}}, RequiresHotel: true}}}
	base, err := Preview(nil, create, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	p := base.Plan
	p.ID = "p"
	depart := time.Date(2026, 10, 26, 8, 0, 0, 0, beijing)
	arrive := depart.Add(time.Hour)
	price := int64(10000)
	p.Tasks[0].Train = &model.TravelSelection{Ref: model.TravelCandidateRef{QueryID: "q", ResultRevision: 1, CandidateID: "g"}, Candidate: model.TravelCandidate{ID: "g", Name: "G1", TrainKind: "high_speed", SeatClass: "二等座", DepartureAt: &depart, ArrivalAt: &arrive, PriceFen: &price}}
	p.Tasks[0].Hotel = &model.TravelSelection{Candidate: model.TravelCandidate{Name: "地点酒店", City: "杭州"}}
	t.Run("fractional_nightly_budget_not_truncated", func(t *testing.T) {
		q, _ := clone(p)
		price, budget := int64(15001), int64(7500)
		q.Tasks[0].Hotel.Candidate.PriceFen = &price
		q.Tasks[0].Nights = 2
		q.Tasks[0].Constraints.MaxHotelNightFen = &budget
		v, _, err := validatePlan(q)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, issue := range v.Issues {
			if issue.Code == "hotel_budget_failed" && issue.Blocking {
				found = true
			}
		}
		if !found {
			t.Fatal("per-night fractional fen silently rounded down")
		}
	})
	t.Run("structured_rule_error_keeps_correction_reason", func(t *testing.T) {
		err := ValidateConditions(sourceConditions(`{"unknown":"value"}`))
		var coded interface{ RuleCode() string }
		if !errors.As(err, &coded) || coded.RuleCode() != "INVALID_ARGUMENTS" || !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("specific rule error lost: %T %v", err, err)
		}
	})
	t.Run("hotel_place_is_nonblocking_without_quote_requirement", func(t *testing.T) {
		out, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main"}}, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Validation.Status != "complete" {
			t.Fatalf("ordinary tourism should complete with explicit hotel-price caveat: %+v", out.Validation)
		}
	})
	t.Run("repair_codes_must_reference_current_related_issues", func(t *testing.T) {
		_, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "verifier"}, RepairIssueCodes: []string{"invented-conflict"}}, nil, now)
		if err == nil {
			t.Fatal("stale or unrelated repair accepted")
		}
	})
	t.Run("model_cannot_invent_hotel_quote_requirement", func(t *testing.T) {
		cmd := create
		ctx := *create.Context
		ctx.OriginalQuery = "上海到杭州，安排住宿"
		cmd.Context = &ctx
		cmd.Tasks = []model.TripTask{create.Tasks[0]}
		cmd.Tasks[0].Conditions = sourceConditions(`{"departure_date":"2026-10-26","check_in":"2026-10-26","check_out":"2026-10-28","hotel_quote_required":true}`)
		if _, err := Preview(nil, cmd, nil, now); err == nil {
			t.Fatal("model invented a blocking quote requirement without user evidence")
		}
	})
	t.Run("hard_date_conflict_does_not_launch_useless_queries", func(t *testing.T) {
		cmd := create
		cmd.ConfirmedConditions = sourceConditions(`{"start_date":"2026-10-26","end_date":"2026-10-25"}`)
		out, err := Preview(nil, cmd, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Plan.StopReason != "hard_constraint_conflict" || len(out.Queries) != 0 {
			t.Fatalf("raw hard conflict queried anyway: %+v", out)
		}
	})
	t.Run("all_current_candidates_prove_hard_activity_conflict", func(t *testing.T) {
		q, _ := clone(p)
		late := arrive.Add(7 * time.Hour)
		q.Tasks[0].Activities = []model.TripActivity{{ID: "hard-meeting", Purpose: "会议", Description: "固定下午会议", Date: "2026-10-26", Time: "14:00", Source: "user", Strength: model.Hard}}
		q.QueryResults = map[string]model.TravelEvidence{"q": {ID: "q", TaskID: q.Tasks[0].ID, Status: "ok", Query: model.TravelQuery{Domain: "train", Origin: q.Tasks[0].Origin, Destination: q.Tasks[0].Destination, Date: q.Tasks[0].DepartureDate, Passengers: 1, TaskRevision: q.Tasks[0].Revision}, Candidates: []model.TravelCandidate{{Name: "G1", DepartureAt: &depart, ArrivalAt: &late}}}}
		v, _, err := validatePlan(q)
		if err != nil {
			t.Fatal(err)
		}
		conflict := func(v model.TripValidation) bool {
			for _, issue := range v.Issues {
				if issue.Code == "hard_date_conflict" {
					return true
				}
			}
			return false
		}
		if !conflict(v) {
			t.Fatal("proven current hard-date conflict missing")
		}
		e := q.QueryResults["q"]
		e.Candidates = append(e.Candidates, model.TravelCandidate{Name: "G2", DepartureAt: &depart, ArrivalAt: &arrive})
		q.QueryResults["q"] = e
		v, _, err = validatePlan(q)
		if err != nil || conflict(v) {
			t.Fatal("one bad selection must not prove all candidates impossible")
		}
	})
	t.Run("hotel_conditions_do_not_invalidate_train", func(t *testing.T) {
		out, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main"}, Changes: []model.TripTaskChange{{TaskID: p.Tasks[0].ID, Conditions: sourceConditions(`{"check_out":"2026-10-29"}`), Components: []string{"hotel"}}}}, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Plan.Tasks[0].Train == nil || out.Plan.Tasks[0].Hotel != nil {
			t.Fatal("hotel-only change invalidated unrelated train or retained stale hotel")
		}
	})
	t.Run("component_scope_is_enforced", func(t *testing.T) {
		activities := []model.TripActivity{{Purpose: "游览", Description: "游览西湖", Source: "proposal", Strength: model.Soft}}
		_, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main"}, Changes: []model.TripTaskChange{{TaskID: p.Tasks[0].ID, Activities: &activities, Components: []string{"train"}}}}, nil, now)
		if err == nil {
			t.Fatal("content change outside component scope accepted")
		}
	})
	t.Run("global_hard_budget_cannot_be_overridden_by_task", func(t *testing.T) {
		cmd := create
		cmd.ConfirmedConditions = sourceConditions(`{"constraints":{"train_max_total_cny":200}}`)
		cmd.Tasks = []model.TripTask{create.Tasks[0]}
		cmd.Tasks[0].Conditions = sourceConditions(`{"departure_date":"2026-10-26","constraints":{"train_max_total_cny":400}}`)
		if _, err := Preview(nil, cmd, nil, now); err == nil {
			t.Fatal("task overrode inherited hard budget")
		}
	})
	t.Run("coarse_activity_has_no_invented_deadline", func(t *testing.T) {
		activities := []model.TripActivity{{Purpose: "会议", Description: "上午会议", Date: "2026-10-26", Time: "上午", Source: "user", Strength: model.Hard}}
		out, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main"}, Changes: []model.TripTaskChange{{TaskID: p.Tasks[0].ID, Activities: &activities}}}, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Plan.Tasks[0].ArrivalBefore != "" || out.Plan.Tasks[0].Activities[0].Time != "上午" {
			t.Fatal("coarse activity precision changed")
		}
		found := false
		for _, issue := range out.Validation.Issues {
			if issue.Code == "activity_window_unverified" && !issue.Blocking {
				found = true
			}
		}
		if !found {
			t.Fatal("partially overlapping coarse window must stay explicitly unverified")
		}
	})
	t.Run("trip_controls_are_not_platform_task_controls", func(t *testing.T) {
		out, err := Preview(&p, model.TripCommand{Operation: "pause", PlanID: p.ID, ExpectedRevision: p.Revision, Reason: "用户稍后安排", Context: &model.TripDecisionContext{Role: "main"}}, nil, now)
		if err != nil || out.Plan.StopReason != "paused" {
			t.Fatalf("business pause lost: %+v %v", out.Plan, err)
		}
	})
	t.Run("full_route_preserves_omitted_components_and_queries_only_new_items", func(t *testing.T) {
		origin, destination, purpose := "杭州", "南京", "visit"
		items := []model.TripQueryItem{{Domain: "train"}}
		rows := []model.TripTaskChange{{TaskID: p.Tasks[0].ID}, {Origin: &origin, Destination: &destination, Purpose: &purpose, Conditions: sourceConditions(`{"departure_date":"2026-10-29"}`), Items: &items}}
		out, err := Preview(&p, model.TripCommand{Operation: "amend", PlanID: p.ID, ExpectedRevision: p.Revision, Context: &model.TripDecisionContext{Role: "main"}, RouteTasks: &rows}, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Plan.Tasks) != 2 || !out.Plan.Tasks[0].RequiresHotel || out.Plan.Tasks[0].Hotel == nil || out.Plan.Tasks[0].Train == nil || out.Plan.Tasks[1].ID == out.Plan.Tasks[0].ID {
			t.Fatalf("route replacement lost omitted data: %+v", out.Plan.Tasks)
		}
		if len(out.Queries) != 1 || out.Queries[0].Origin != "杭州" {
			t.Fatalf("route replacement repeated unchanged queries: %+v", out.Queries)
		}
	})
}
