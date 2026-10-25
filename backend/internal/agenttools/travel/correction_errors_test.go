package travel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent-platform/backend/internal/model"
)

func TestTravelCorrectionFeedback(t *testing.T) {
	for _, tc := range []struct{ name, args, code, hint string }{
		{"business_requires_policy_not_visit", `{"request_kind":"planning","tripplan":{"operation":"propose","tasks":[{"origin":"上海","destination":"杭州","purpose":"business","conditions":{"departure_date":"2026-11-01"}}]}}`, "POLICY_OBSERVATION_REQUIRED", "query_policy"},
		{"arrival_deadline_has_exact_correct_path", `{"request_kind":"planning","tripplan":{"operation":"propose","tasks":[{"origin":"上海","destination":"杭州","conditions":{"departure_date":"2026-11-01","constraints":{"arrival_before":"2026-11-01T13:00:00+08:00"}}}]}}`, "INVALID_ARGUMENTS", "tripplan.tasks[0].conditions.arrival_before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trips := &sourceTripsFixture{}
			r, err := NewRegistry(NativeConfig{Config: Config{Trips: trips}})
			if err != nil {
				t.Fatal(err)
			}
			batch, err := r.Preflight(context.Background(), ProfileRequest{Role: "main"}, []NativeCall{nativeCall("read", "read", `{"request_kind":"planning","resource":"trip_summaries"}`), nativeCall("bad", "write_trip", tc.args)})
			var rule *model.TravelRuleError
			if !errors.As(err, &rule) {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(rule)
			if rule.Code != tc.code || !strings.Contains(string(raw), tc.hint) {
				t.Errorf("not actionable: %s", raw)
			}
			data, _ := json.Marshal(batch)
			var b map[string]any
			_ = json.Unmarshal(data, &b)
			if b["failed_call_id"] != "bad" {
				t.Error("preflight lost root call identity", string(data))
			}
			if trips.writes.Load() != 0 {
				t.Fatal("rejected batch executed a write")
			}
		})
	}
}
