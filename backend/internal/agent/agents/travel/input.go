package travel

import (
	"encoding/json"
	"slices"
	"strings"

	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
)

// RunInput already contains only platform-authorized context. Do not scan raw
// historical storage here: replaced runs may be visible but are not context.
func sourceInput(in contracts.RunInput) (string, []string) {
	query := ""
	ids := []string{}
	previousRun := ""
	for i := len(in.Messages) - 1; i >= 0; i-- {
		m := in.Messages[i]
		if query == "" && m.Role == "user" && (m.RunID == in.Run.ID || m.RunID == "") {
			query = m.Content
		}
		if in.Resume || m.Role != "tool" || m.RunID == in.Run.ID {
			continue
		}
		var view struct {
			ID   string `json:"trip_plan_id"`
			Trip struct {
				Plan struct {
					ID string `json:"id"`
				} `json:"plan"`
			} `json:"trip"`
		}
		if json.Unmarshal([]byte(m.Content), &view) != nil {
			continue
		}
		id := view.ID
		if id == "" {
			id = view.Trip.Plan.ID
		}
		if id == "" {
			continue
		}
		if previousRun == "" {
			previousRun = m.RunID
		}
		if m.RunID != previousRun {
			break
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return query, ids
}

func sourceAdjustmentHistory(messages []pm.Message, plans map[string]pm.TripResult) []json.RawMessage {
	results := map[string]pm.TravelToolResult{}
	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		var r pm.TravelToolResult
		if json.Unmarshal([]byte(msg.Content), &r) == nil && r.Name == "write_trip" {
			results[msg.RunID+"/"+msg.ToolCallID] = r
		}
	}
	rows := []json.RawMessage{}
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Name != "write_trip" {
				continue
			}
			result, ok := results[msg.RunID+"/"+call.ID]
			if !ok {
				continue
			}
			current, known := plans[result.TripPlanID]
			if !known {
				continue
			}
			var args struct {
				Tripplan map[string]json.RawMessage `json:"tripplan"`
				Summary  string                     `json:"round_summary"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) != nil {
				continue
			}
			changes := map[string]json.RawMessage{}
			for k, v := range args.Tripplan {
				if !slices.Contains([]string{"operation", "trip_plan_id", "expected_revision", "reason"}, k) {
					changes[k] = v
				}
			}
			summary := args.Summary
			if summary == "" {
				summary = msg.Content
			}
			runes := []rune(summary)
			if len(runes) > 300 {
				summary = string(runes[:300])
			}
			effect := "not_applied"
			if result.Revision > 0 {
				effect = "saved_draft"
				if result.Status == "completed" {
					effect = "committed"
				}
			} else if result.Status == "unknown" || result.Status == "error" {
				effect = "unknown"
			}
			rows = append(rows, mustMarshal(map[string]any{"call_id": call.ID, "session_id": msg.ConversationID, "operation": result.Operation, "summary": strings.TrimSpace(summary), "reason": args.Tripplan["reason"], "result_reason": result.Reason, "status": result.Status, "effect": effect, "revision": result.Revision, "historical_revision": result.Revision > 0 && result.Revision < current.Plan.Revision, "changes": changes}))
		}
	}
	return rows
}
