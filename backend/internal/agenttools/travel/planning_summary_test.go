package travel

import (
	"context"
	"strings"
	"testing"
)

func TestPlanningSummaryProfileAndScope(t *testing.T) {
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: &sourceTripsFixture{}}})
	if err != nil {
		t.Fatal(err)
	}
	req := ProfileRequest{Role: "planning_summary", AllowedTripIDs: []string{"p"}, PolicyObserved: true, PlanContextComplete: true}
	tools, err := r.Profile(req)
	if err != nil {
		t.Fatal("planning summary has no actual repair tools", err)
	}
	for _, tool := range tools {
		if tool.Name != "write_trip" {
			t.Fatalf("unnecessary summary tool: %s", tool.Name)
		}
	}
	args := `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"route_tasks":[{"origin":"上海","destination":"南京","conditions":{"departure_date":"2099-10-28"}},{"task_id":"t1","origin":"南京","destination":"杭州","conditions":{"departure_date":"2099-10-28"}}]}}`
	batch, err := r.Preflight(context.Background(), req, []NativeCall{nativeCall("repair", "write_trip", args)})
	if err != nil || len(batch.Calls) != 1 || batch.Calls[0].Trip.Context.Role != "planning_summary" {
		t.Fatalf("summary cannot directly repair: %+v %v", batch, err)
	}
	for _, bad := range []string{
		strings.Replace(args, `"trip_plan_id":"p"`, `"trip_plan_id":"other"`, 1),
		sourceProposal,
		`{"request_kind":"planning","tripplan":{"operation":"cancel","trip_plan_id":"p","expected_revision":2,"reason":"stop"}}`,
	} {
		if _, err := r.Preflight(context.Background(), req, []NativeCall{nativeCall("bad", "write_trip", bad)}); err == nil {
			t.Fatal("summary exceeded current plan repair scope", bad)
		}
	}
	if _, err := r.Preflight(context.Background(), req, []NativeCall{nativeCall("handoff", "request_replan", `{"request_kind":"planning","trip_plan_ids":["p"],"reason":"route"}`)}); err == nil {
		t.Fatal("summary still hands back to Main")
	}
	legacy := req
	legacy.Role = "verifier"
	if _, err := r.Preflight(context.Background(), legacy, []NativeCall{nativeCall("old", "write_trip", args)}); err == nil {
		t.Fatal("legacy verifier authority silently changed")
	}
	recovery := ProfileRequest{Role: "planning_summary", NewPlanningRequest: true}
	if _, err := r.Preflight(context.Background(), recovery, []NativeCall{nativeCall("recover", "write_trip", sourceProposal)}); err != nil {
		t.Fatal("summary cannot recover rejected initial plan", err)
	}
}
