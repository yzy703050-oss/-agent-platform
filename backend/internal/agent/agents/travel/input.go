package travel

import (
	"encoding/json"
	"regexp"
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
	if !in.Resume {
		if provided := explicitTripIDs(query); len(provided) > 0 {
			return query, provided
		}
	}
	return query, ids
}

var tripIDMention = regexp.MustCompile(`(?i)行程\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\b`)
var tripEditIntent = regexp.MustCompile(`修改|调整|更改|改为|改成|改到|取消|删除|重排|换成`)

// An explicit current-request edit may identify its own trip. The execution
// still uses Inspect to verify scope and provenance before the first model call;
// this function performs no I/O and does not recover context from old user text.
func explicitTripIDs(query string) []string {
	edit := false
	positive := []string{}
	for _, clause := range strings.FieldsFunc(query, func(r rune) bool { return strings.ContainsRune("，。；,;\n", r) }) {
		negated := false
		for _, word := range []string{"不要", "不修改", "不调整", "无需", "不用", "不必", "假设", "假如", "例如", "示例"} {
			negated = negated || strings.Contains(clause, word)
		}
		if !negated {
			positive = append(positive, clause)
			edit = edit || tripEditIntent.MatchString(clause)
		}
	}
	if !edit {
		return nil
	}
	ids := []string{}
	for _, match := range tripIDMention.FindAllStringSubmatch(strings.Join(positive, "，"), -1) {
		id := strings.ToLower(match[1])
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
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
