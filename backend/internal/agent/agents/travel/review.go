package travel

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
)

// Review is a projection of durable facts. Full tool bodies remain in the
// journal/checkpoint; repeating them here changes the source model's context.
func sourceObject(v any) map[string]any {
	raw, _ := json.Marshal(v)
	if b, ok := v.(json.RawMessage); ok {
		raw = b
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func sourceChanges(plan map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range plan {
		if !slices.Contains([]string{"operation", "trip_plan_id", "expected_revision", "reason"}, k) {
			out[k] = v
		}
	}
	return out
}

func sourceCandidateView(value any) map[string]any {
	view := sourceObject(value)
	for _, k := range []string{"cache_hit", "excluded_candidate_count", "returned_candidate_total"} {
		delete(view, k)
	}
	items, _ := view["items"].([]any)
	for _, value := range items {
		item, _ := value.(map[string]any)
		if view["domain"] == "train" {
			for _, kind := range []string{"departure", "arrival"} {
				stamp, _ := item[kind+"_at"].(string)
				at, err := time.Parse(time.RFC3339Nano, stamp)
				if err != nil {
					continue
				}
				period := "晚上"
				switch {
				case at.Hour() < 6:
					period = "凌晨"
				case at.Hour() < 12:
					period = "上午"
				case at.Hour() < 18:
					period = "下午"
				}
				item[kind+"_period"] = period
				if item[kind+"_time"] == at.Format("15:04") {
					delete(item, kind+"_time")
				}
			}
		}
		if view["domain"] == "hotel" && view["unverified_constraints"] == nil {
			for k, v := range item {
				if v == nil {
					delete(item, k)
				}
			}
		}
	}
	if view["domain"] == "hotel" && view["unverified_constraints"] == nil {
		delete(view, "message")
	}
	return view
}

func resultSummary(r pm.TravelToolResult) map[string]any {
	out := sourceObject(r)
	for key := range out {
		if !slices.Contains([]string{"call_id", "name", "status", "reason", "stop_reason", "trip_plan_id", "revision", "error", "selection_saved"}, key) {
			delete(out, key)
		}
	}
	// selection_saved belongs to the native observation, not the platform header.
	if value, ok := sourceObject(r.Raw)["selection_saved"]; ok {
		out["selection_saved"] = value
	}
	return out
}

func previousSourceRound(s FlowState) (map[string]any, map[string]any) {
	rows := []any{}
	attempted := map[string]any{}
	for _, call := range s.LastBatchCalls {
		row := map[string]any{"name": call.Name, "call_id": call.ID}
		args := sourceObject(call.Arguments)
		if call.Name == "write_trip" {
			if plan, ok := args["tripplan"].(map[string]any); ok {
				attempted = plan
				if id, ok := plan["trip_plan_id"]; ok {
					row["requested_trip_plan_id"] = id
				}
			}
		}
		for _, r := range s.LastBatchResults {
			if r.CallID == call.ID {
				for k, v := range resultSummary(r) {
					row[k] = v
				}
			}
		}
		rows = append(rows, row)
	}
	summary := []rune(s.PreviousRoundSummary)
	if len(summary) > 300 {
		summary = summary[:300]
	}
	return map[string]any{"summary": string(summary), "results": rows}, attempted
}

func sourcePlanReview(s FlowState, r pm.TripResult) map[string]any {
	view := sourceObject(s.PlanView(r)) // isolate maps before removing duplicate fields
	if candidates, ok := view["candidates"].([]any); ok {
		for i, v := range candidates {
			candidates[i] = sourceCandidateView(v)
		}
	}
	if r.Plan.Status == "completed" {
		view["candidates"] = []any{}
	}
	if snapshot, ok := view["constraint_snapshot"].(map[string]any); ok {
		policy, _ := view["policy_context"].(map[string]any)
		constraint, _ := snapshot["policy"].(map[string]any)
		match := len(policy) > 0 && len(constraint) > 0
		fields := []string{}
		for k, v := range constraint {
			fields = append(fields, k)
			if !reflect.DeepEqual(policy[k], v) {
				match = false
			}
		}
		slices.Sort(fields)
		if match {
			snapshot["policy"] = map[string]any{"ref": "tripplan.policy_context", "fields": fields}
		}
	}
	if tasks, ok := view["tasks"].([]any); ok {
		for _, value := range tasks {
			task, _ := value.(map[string]any)
			if task["draft_plan"] == nil {
				delete(task, "draft_plan")
			}
			// Omit inherited query parameters only when values AND provenance agree.
			conditions, _ := task["conditions"].(map[string]any)
			if items, ok := task["items"].([]any); ok {
				for _, value := range items {
					item, _ := value.(map[string]any)
					parameters, _ := item["parameters"].(map[string]any)
					for k, v := range parameters {
						inherited, exists := conditions[k]
						if !exists {
							inherited = task[k]
						}
						if k == "city" {
							inherited = task["destination"]
						}
						same := v != nil && reflect.DeepEqual(v, inherited)
						for _, meta := range []string{"field_sources", "condition_strengths"} {
							im, _ := item[meta].(map[string]any)
							tm, _ := task[meta].(map[string]any)
							if iv, present := im[k]; present && !reflect.DeepEqual(iv, tm[k]) {
								same = false
							}
						}
						if same {
							delete(parameters, k)
						}
					}
				}
			}
		}
	}
	return view
}

func sourceReplyFacts(s FlowState, r pm.TripResult, view map[string]any) map[string]any {
	unverified := []any{}
	if pending, ok := view["pending_queries"].([]any); ok {
		for _, row := range pending {
			unverified = append(unverified, row)
		}
	}
	for _, e := range r.Plan.QueryResults {
		if slices.Contains(s.BlockedEvidence, pm.TravelCandidateRef{QueryID: e.ID, ResultRevision: e.ResultRevision}) {
			unverified = append(unverified, map[string]any{"task_id": e.Query.TaskID, "domain": e.Query.Domain, "reason": "needs_revalidation"})
		}
	}
	var verified any
	if len(unverified) > 0 {
		verified = false
	}
	return map[string]any{"old_plan_preserved": view["committed_revision"] != nil, "booking_performed": false, "reservation_performed": false, "current_data_verified": verified, "unverified_components": unverified}
}

func buildSourceReview(s FlowState) map[string]any {
	previous, attempted := previousSourceRound(s)
	plans := []any{}
	facts := []any{}
	current := map[string]map[string]any{}
	for _, id := range s.planIDs() {
		r := s.Plans[id]
		view := sourcePlanReview(s, r)
		current[id] = view
		plans = append(plans, view)
		f := sourceReplyFacts(s, r, view)
		f["trip_plan_id"] = id
		facts = append(facts, f)
	}
	history := []any{}
	for _, raw := range s.AdjustmentHistory {
		row := sourceObject(raw)
		if row["operation"] == "propose" && len(plans) > 0 {
			row["changes"] = map[string]any{"initial_plan_in_current_snapshot": true}
		}
		history = append(history, row)
	}
	queries := []any{}
	memory := []any{}
	observations := []any{}
	for _, r := range s.Results {
		switch r.Name {
		case "query":
			if r.Error != nil && r.Error.Code == "UNKNOWN_TOOL" {
				continue
			}
			var views []any
			_ = json.Unmarshal(r.Data, &views)
			for i, value := range views {
				projected := sourceCandidateView(value)
				bindings := []string{}
				currentItems := []any{}
				for _, id := range s.planIDs() {
					plan := current[id]
					queries, _ := plan["candidate_queries"].(map[string]any)
					qid, _ := projected["query_id"].(string)
					if !reflect.DeepEqual(queries[qid], projected["result_revision"]) {
						continue
					}
					bindings = append(bindings, qid)
					candidates, _ := plan["candidates"].([]any)
					for _, candidate := range candidates {
						c, _ := candidate.(map[string]any)
						if c["query_id"] == qid {
							items, _ := c["items"].([]any)
							currentItems = append(currentItems, items...)
						}
					}
				}
				if len(bindings) > 0 {
					items, _ := projected["items"].([]any)
					unique := []any{}
					order := []any{}
					for _, item := range items {
						row, _ := item.(map[string]any)
						order = append(order, row["id"])
						if !slices.ContainsFunc(currentItems, func(v any) bool { return reflect.DeepEqual(v, item) }) {
							unique = append(unique, item)
						}
					}
					delete(projected, "items")
					if len(unique) > 0 {
						projected["items"] = unique
					}
					projected["candidate_order"] = order
					projected["candidates_in_tripplan"] = true
				}
				projected["task_bindings"] = bindings
				views[i] = projected
			}
			queries = append(queries, map[string]any{"call_id": r.CallID, "status": r.Status, "reason": r.Reason, "data": views})
		case "write_memory":
			memory = append(memory, r)
		case "read", "query_policy":
			obs := sourceObject(r)
			data := sourceObject(r.Data)
			for id, plan := range current {
				if r.Name == "query_policy" && reflect.DeepEqual(data, plan["policy_context"]) {
					obs["data"] = map[string]any{"policy_in_tripplan": true}
					break
				}
				if r.Name == "read" && (data["trip_plan_id"] == id || data["id"] == id) {
					obs["data"] = map[string]any{"trip_plan_id": id, "in_current_snapshot": true}
					break
				}
			}
			observations = append(observations, obs)
		}
	}
	execution := []any{}
	for _, job := range s.Jobs {
		if job.Result != nil {
			row := map[string]any{"call_id": job.ID, "parent_call_id": job.ParentCallID, "name": "query", "status": job.Result.Status, "trip_plan_id": job.Query.PlanID}
			if job.Result.Status != "ok" {
				v := traveltools.NativeEvidenceView(*job.Result)
				failure := map[string]any{}
				for _, k := range []string{"domain", "status", "reason", "message", "error"} {
					if value, ok := v[k]; ok {
						failure[k] = value
					}
				}
				row["query_outcomes"] = []any{failure}
			}
			execution = append(execution, row)
		}
	}
	view := map[string]any{"original_query": s.OriginalQuery, "tripplan": attempted, "reply_facts": map[string]any{}, "query_results": queries, "observations": observations, "memory_results": memory, "previous_round_summary": previous, "adjustment_history": history, "execution_results": execution}
	if len(plans) == 1 {
		view["tripplan"] = plans[0]
		f := facts[0].(map[string]any)
		delete(f, "trip_plan_id")
		view["reply_facts"] = f
	} else if len(plans) > 1 {
		view["tripplan"] = map[string]any{}
		view["tripplans"] = plans
		view["reply_facts"] = facts
	}
	for i := len(s.Results) - 1; i >= 0; i-- {
		r := s.Results[i]
		if r.Name == "request_replan" && r.Status == "ok" {
			view["replan_request"] = resultSummary(r)
			view["replan_request"].(map[string]any)["trip_plan_ids"] = r.TripPlanIDs
			break
		}
	}
	return view
}
