package travel

import (
	"agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/cloudwego/eino/schema"
	"slices"
	"strings"
	"testing"
)

type reviewErrorTrips struct {
	sourceTripsFixture
	failure error
}

func (f *reviewErrorTrips) Prepare(context.Context, model.TravelExecution, model.TripCommand) (model.TripResult, error) {
	return model.TripResult{}, f.failure
}

func TestTravelReviewReplanFeedback(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		failure    error
	}{
		{"route_requires_main", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"route_tasks":[{"task_id":"t1","origin":"上海","destination":"南京","conditions":{"departure_date":"2026-10-26"}},{"origin":"南京","destination":"杭州","conditions":{"departure_date":"2026-10-26"}}]}}`, nil},
		{"route_authority_before_leaf_metadata", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"route_tasks":[{"task_id":"t1","origin":"上海","destination":"杭州","conditions":{"constraints":{"seat_class":"二等座","train_kind":"high_speed"}},"field_sources":{"constraints":"proposal"}}]}}`, nil},
		{"hard_activity_reassignment_requires_main", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t1","components":["content"],"content":[{"id":"belongs_to_other_task","purpose":"游览灵隐寺","description":"上午在杭州游览灵隐寺","date":"2026-10-27","time":"上午","source":"user","strength":"hard"}]}]}}`, &model.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "activity identity is unknown or repeated"}},
		{"hard_stay_cannot_be_softened", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t1","conditions":{"nights":2},"condition_strengths":{"nights":"soft"}}]}}`, &model.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "cannot soften hard task condition"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewRegistry(NativeConfig{Config: Config{Trips: &reviewErrorTrips{failure: tc.failure}}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.Preflight(context.Background(), ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("repair", "write_trip", tc.args)})
			var rule *model.TravelRuleError
			if !errors.As(err, &rule) || rule.Code != "REPLAN_REQUIRED" || !strings.Contains(rule.Message, "request_replan") {
				t.Fatalf("no actionable Main handoff: %v", err)
			}
		})
	}
}

func TestTravelReviewHandoffProfile(t *testing.T) {
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: &sourceTripsFixture{}}})
	if err != nil {
		t.Fatal(err)
	}
	request := ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}, ReplanRequired: true}
	tools, err := r.Profile(request)
	if err != nil || len(tools) != 1 || tools[0].Name != "request_replan" {
		t.Fatalf("review authority did not narrow to handoff: tools=%v err=%v", tools, err)
	}
	_, err = r.Preflight(context.Background(), request, []NativeCall{nativeCall("repeat", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t1","components":["content"],"content":[]}]}}`)})
	var rule *model.TravelRuleError
	if !errors.As(err, &rule) || rule.Code != "REPLAN_REQUIRED" {
		t.Fatalf("repeated mutation accepted: %v", err)
	}
	_, err = r.Preflight(context.Background(), request, []NativeCall{nativeCall("handoff", "request_replan", `{"request_kind":"planning","trip_plan_ids":["p"],"reason":"硬活动归属需Main修复"}`)})
	if err != nil {
		t.Fatalf("valid handoff rejected: %v", err)
	}
}

func TestTravelMainRouteRepairFeedback(t *testing.T) {
	for _, message := range []string{"route task identity unknown or repeated", "activity identity is unknown or repeated", "route is not continuous"} {
		cmd := model.TripCommand{RouteTasks: &[]model.TripTaskChange{}}
		err := reviewRepairError("main", cmd, &model.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: message})
		var rule *model.TravelRuleError
		if !errors.As(err, &rule) || rule.Code != "INVALID_ARGUMENTS" || rule.Message == message || !strings.Contains(rule.Message, "route_tasks") {
			t.Fatalf("Main received no actionable route feedback: %v", err)
		}
	}
}

func TestTravelMainUsesProvidedPlanContext(t *testing.T) {
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: &sourceTripsFixture{}}})
	if err != nil {
		t.Fatal(err)
	}
	request := ProfileRequest{Role: "main", PolicyObserved: true, PlanContextComplete: true}
	tools, err := r.Profile(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range tools {
		if info.Name == "read" {
			t.Fatal("replan still exposes redundant history reads")
		}
	}
	_, err = r.Preflight(context.Background(), request, []NativeCall{nativeCall("read", "read", `{"request_kind":"planning","resource":"trip","trip_plan_id":"p"}`)})
	var rule *model.TravelRuleError
	if !errors.As(err, &rule) || rule.Code != "CONTEXT_ALREADY_PROVIDED" {
		t.Fatalf("redundant read accepted: %v", err)
	}
	request.PlanContextComplete = false
	tools, err = r.Profile(request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(tools, func(info *schema.ToolInfo) bool { return info.Name == "read" }) {
		t.Fatal("explicit historical lookup lost its tool")
	}
}

func TestTravelNewPlanningProfileDoesNotReadHistory(t *testing.T) {
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: &sourceTripsFixture{}}})
	if err != nil {
		t.Fatal(err)
	}
	request := ProfileRequest{Role: "main", NewPlanningRequest: true}
	tools, err := r.Profile(request)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(tools, func(info *schema.ToolInfo) bool { return info.Name == "read" }) {
		t.Fatal("new request can still read history for preferences")
	}
	_, err = r.Preflight(context.Background(), request, []NativeCall{nativeCall("read", "read", `{"request_kind":"lookup","resource":"trip_summaries"}`)})
	var rule *model.TravelRuleError
	if !errors.As(err, &rule) || rule.Code != "HISTORY_NOT_REQUESTED" {
		t.Fatalf("proactive history read accepted: %v", err)
	}
}
