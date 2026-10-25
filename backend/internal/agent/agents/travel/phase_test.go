package travel

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
)

func TestTravelPhaseTransitions(t *testing.T) {
	// Frozen Python runtime/state.py TRANSITIONS; both absent and present edges
	// matter, especially terminal states and correction after a rejected batch.
	allowed := map[string]string{
		"deciding":        "executing reply_ready stopping paused",
		"executing":       "validating stopping paused",
		"validating":      "deciding repair_required reply_ready stopping paused",
		"repair_required": "deciding executing reply_ready stopping paused",
		"stopping":        "reply_ready paused", "reply_ready": "completed stopped",
		"completed": "", "stopped": "", "paused": "",
	}
	for from, targets := range allowed {
		for to := range allowed {
			s := FlowState{Phase: from}
			err := s.transition(to)
			valid := from == to || strings.Contains(" "+targets+" ", " "+to+" ")
			if valid {
				if err != nil || s.Phase != to {
					t.Errorf("legal %s→%s: phase=%s err=%v", from, to, s.Phase, err)
				}
				continue
			}
			var rule *pm.TravelRuleError
			if !errors.As(err, &rule) || rule.Code != "INVALID_STATE_TRANSITION" || s.Phase != from {
				t.Errorf("illegal %s→%s mutated or accepted: phase=%s err=%v", from, to, s.Phase, err)
				continue
			}
			var gotFrom, gotTo string
			_ = json.Unmarshal(rule.Details["from"], &gotFrom)
			_ = json.Unmarshal(rule.Details["to"], &gotTo)
			if gotFrom != from || gotTo != to {
				t.Error("transition error lost endpoints")
			}
		}
	}
	for _, pair := range [][2]string{{"unknown", "deciding"}, {"deciding", "unknown"}, {"unknown", "unknown"}} {
		s := FlowState{Phase: pair[0]}
		var rule *pm.TravelRuleError
		if !errors.As(s.transition(pair[1]), &rule) || rule.Code != "INVALID_EXECUTION_STATE" || s.Phase != pair[0] {
			t.Error("unknown phase accepted")
		}
	}
	s := FlowState{Phase: "deciding"}
	if err := s.stop("first"); err != nil {
		t.Fatal(err)
	}
	if err := s.stop("second"); err != nil || s.Phase != "stopping" || s.StopReason != "first" {
		t.Fatal("stop is not idempotent", s, err)
	}
	if err := s.transition("reply_ready"); err != nil {
		t.Fatal(err)
	}
	if err := s.stop("third"); err != nil || s.StopReason != "first" || s.Phase != "reply_ready" {
		t.Fatal("ready reply was reopened", err)
	}
	for _, phase := range []string{"stopping", "reply_ready", "completed", "stopped", "paused"} {
		s := FlowState{Phase: phase, PlanningRounds: 2}
		before := s
		err := s.ReserveBatch([]traveltools.NativeCall{{Name: "write_trip"}}, 8)
		var rule *pm.TravelRuleError
		if !errors.As(err, &rule) || rule.Code != "EXECUTION_STOPPED" || !reflect.DeepEqual(s, before) {
			t.Errorf("summary/terminal %s accepted or mutated tools", phase)
		}
	}
}
