package travel

import (
	"encoding/json"
	"slices"

	pm "agent-platform/backend/internal/model"
)

// Mirrors frozen Python runtime/state.py; Run and TripPlan states are separate.
var phaseTransitions = map[string][]string{
	"deciding":        {"executing", "reply_ready", "stopping", "paused"},
	"executing":       {"validating", "stopping", "paused"},
	"validating":      {"deciding", "repair_required", "reply_ready", "stopping", "paused"},
	"repair_required": {"deciding", "executing", "reply_ready", "stopping", "paused"},
	"stopping":        {"reply_ready", "paused"},
	"reply_ready":     {"completed", "stopped"},
	"completed":       {},
	"stopped":         {},
	"paused":          {},
}

func knownPhase(phase string) bool { _, ok := phaseTransitions[phase]; return ok }

func (s *FlowState) transition(next string) error {
	if !knownPhase(s.Phase) || !knownPhase(next) {
		return &pm.TravelRuleError{Code: "INVALID_EXECUTION_STATE", Message: "unknown execution phase"}
	}
	if next == s.Phase {
		return nil
	}
	if !slices.Contains(phaseTransitions[s.Phase], next) {
		return &pm.TravelRuleError{Code: "INVALID_STATE_TRANSITION", Message: "execution phase does not permit this operation", Details: map[string]json.RawMessage{"from": mustMarshal(s.Phase), "to": mustMarshal(next)}}
	}
	s.Phase = next
	return nil
}

func (s *FlowState) stop(reason string) error {
	if s.Phase != "stopping" && s.Phase != "reply_ready" {
		if err := s.transition("stopping"); err != nil {
			return err
		}
	}
	if s.StopReason == "" {
		s.StopReason = reason
	}
	return nil
}

func (s FlowState) summaryOnly() bool {
	return slices.Contains([]string{"stopping", "reply_ready", "completed", "stopped", "paused"}, s.Phase)
}
