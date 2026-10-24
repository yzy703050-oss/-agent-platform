package travel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"agent-platform/backend/internal/model"
)

type sourceTripsFixture struct{ writes atomic.Int64 }

func (f *sourceTripsFixture) Apply(context.Context, model.TravelExecution, string, model.TripCommand) (model.TripResult, error) {
	f.writes.Add(1)
	return model.TripResult{}, nil
}
func (f *sourceTripsFixture) Read(context.Context, model.Scope, string) (model.TripPlan, error) {
	return model.TripPlan{}, nil
}
func (f *sourceTripsFixture) Inspect(context.Context, model.TravelExecution, string) (model.TripResult, error) {
	return model.TripResult{}, nil
}
func (f *sourceTripsFixture) Query(context.Context, model.TravelExecution, model.TravelQuery) (model.TravelEvidence, error) {
	return model.TravelEvidence{}, nil
}
func (f *sourceTripsFixture) Prepare(context.Context, model.TravelExecution, model.TripCommand) (model.TripResult, error) {
	return model.TripResult{}, nil
}

func nativeCall(id, name, args string) NativeCall {
	return NativeCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

const sourceProposal = `{"request_kind":"planning","tripplan":{"operation":"propose","default_round_trip":false,"tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-10-26"},"items":[{"domain":"train"}]}]}}`

func TestTravelSourceBatch(t *testing.T) {
	ctx := context.Background()
	trips := &sourceTripsFixture{}
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: trips}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		profile ProfileRequest
		calls   []NativeCall
		bad     bool
	}{
		{"invalid_second_call_aborts_whole_batch", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "write_trip", sourceProposal), nativeCall("b", "read", `{"request_kind":"planning","resource":"trip","user_id":"forged"}`)}, true},
		{"same_plan_twice", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":1}}`), nativeCall("b", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":1}}`)}, true},
		{"independent_plans", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":1}}`), nativeCall("b", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"q","expected_revision":1}}`)}, false},
		{"policy_before_plan", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "query_policy", `{"request_kind":"planning","context":{}}`), nativeCall("b", "write_trip", sourceProposal)}, true},
		{"memory_and_plan", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "write_memory", `{"request_kind":"planning","updates":[{"op":"add","text":"优先靠窗"}]}`), nativeCall("b", "write_trip", sourceProposal)}, false},
		{"review_cannot_propose", ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("a", "write_trip", sourceProposal)}, true},
		{"review_scope", ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("a", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"other","expected_revision":1}}`)}, true},
		{"review_cannot_write_memory", ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("a", "write_memory", `{"request_kind":"planning","updates":[{"op":"add","text":"靠窗"}]}`)}, true},
		{"replan_alone", ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("a", "request_replan", `{"request_kind":"planning","trip_plan_ids":["p"],"reason":"需要补全路线"}`)}, false},
		{"replan_not_mixed", ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}}, []NativeCall{nativeCall("a", "request_replan", `{"request_kind":"planning","trip_plan_ids":["p"],"reason":"补全路线"}`), nativeCall("b", "write_trip", `{"request_kind":"planning","tripplan":{"operation":"select","trip_plan_id":"p","expected_revision":1}}`)}, true},
		{"request_kind_consistent", ProfileRequest{Role: "main"}, []NativeCall{nativeCall("a", "write_trip", sourceProposal), nativeCall("b", "read", `{"request_kind":"lookup","resource":"trip_summaries"}`)}, true},
		{"summary_has_no_tools", ProfileRequest{Role: "summary"}, []NativeCall{nativeCall("a", "read", `{"request_kind":"lookup","resource":"trip_summaries"}`)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Preflight(ctx, tc.profile, tc.calls)
			if (err != nil) != tc.bad {
				t.Fatalf("preflight error=%v, want rejected=%v", err, tc.bad)
			}
			if trips.writes.Load() != 0 {
				t.Fatal("preflight produced a business side effect")
			}
		})
	}
}

func TestTravelSourceNormalization(t *testing.T) {
	input := json.RawMessage(`{"request_kind":"planning","tripplan":{"tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-10-26","arrival_before":"2026-10-26T14:00:00","constraints":{"train_kind":"high_speed"}},"field_sources":{"constraints":{"train_kind":"proposal"}},"condition_strengths":{"constraints":"soft"},"items":[{"domain":"train"}]}]}}`)
	for i := 0; i < 10; i++ {
		out, err := NormalizeNativeArguments("write_trip", input, "Asia/Shanghai", "main")
		if err != nil {
			t.Fatal(err)
		}
		var args NativeTripArguments
		if err = json.Unmarshal(out, &args); err != nil {
			t.Fatal(err)
		}
		task := args.Tripplan.Tasks[0]
		if args.Tripplan.Operation != "propose" || task.FieldSources["train_kind"] != "proposal" || task.ConditionStrengths["train_kind"] != "soft" || string(task.Conditions["arrival_before"]) != `"2026-10-26T14:00:00+08:00"` {
			t.Fatalf("source normalization failed: %s", out)
		}
	}
	_, err := NormalizeNativeArguments("write_trip", json.RawMessage(`{"tripplan":{"conditions":{"constraints":{"train_kind":"high_speed","seat_class":"二等座"}},"field_sources":{"constraints":"proposal"}}}`), "Asia/Shanghai", "main")
	var rule *model.TravelRuleError
	if !errors.As(err, &rule) || rule.Details["fields"] == nil {
		t.Fatalf("ambiguous metadata must give field correction: %v", err)
	}
	ctx := context.Background()
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: &sourceTripsFixture{}}})
	if err != nil {
		t.Fatal(err)
	}
	original, err := businessQuery(NativeQueryItem{Domain: "train", Parameters: NativeConditions{"origin": json.RawMessage(`"上海"`), "destination": json.RawMessage(`"杭州"`), "departure_date": json.RawMessage(`"2026-10-26"`)}})
	if err != nil {
		t.Fatal(err)
	}
	profile := ProfileRequest{Role: "verifier", AllowedTripIDs: []string{"p"}, RetryQueries: []model.TravelQuery{original}}
	good := nativeCall("q", "query", `{"request_kind":"planning","query_plan":[{"domain":"train","parameters":{"origin":"上海","destination":"杭州","departure_date":"2026-10-26"},"refresh":true}]}`)
	if _, err = r.Preflight(ctx, profile, []NativeCall{good}); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Arguments = json.RawMessage(strings.ReplaceAll(string(good.Arguments), "2026-10-26", "2026-10-27"))
	if _, err = r.Preflight(ctx, profile, []NativeCall{bad}); err == nil {
		t.Fatal("review retry expanded original conditions")
	}
}
