package travel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/sync/errgroup"
)

type NativeMemory interface {
	Update(context.Context, model.TravelExecution, string, model.TravelMemoryCommand) (model.TravelMemoryResult, error)
}
type NativeHistory interface {
	Read(context.Context, model.TravelExecution, model.TravelHistoryRequest) (json.RawMessage, error)
}
type NativePolicy interface {
	Query(context.Context, model.TravelExecution, model.TravelPolicyQuery) (model.SourcePolicyContext, error)
}
type preparedCallKey struct{}

func WithPreparedCall(ctx context.Context, call PreparedCall) context.Context {
	return context.WithValue(ctx, preparedCallKey{}, call)
}

type nativeTool struct {
	registry *Registry
	info     *schema.ToolInfo
}

func (t *nativeTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }
func (t *nativeTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	prepared, ok := ctx.Value(preparedCallKey{}).(PreparedCall)
	if !ok || prepared.Name != t.info.Name || string(prepared.Arguments) != args {
		return "", model.ErrIdempotencyConflict
	}
	return t.registry.Execute(ctx, prepared)
}

func (r *Registry) Tools() ([]tool.BaseTool, error) {
	main, err := SourceProfile("main")
	if err != nil {
		return nil, err
	}
	review, err := SourceProfile("review")
	if err != nil {
		return nil, err
	}
	infos := append(main, review...)
	out := []tool.BaseTool{}
	seen := map[string]bool{}
	for _, info := range infos {
		if !seen[info.Name] {
			seen[info.Name] = true
			out = append(out, &nativeTool{registry: r, info: info})
		}
	}
	return out, nil
}

func (r *Registry) Execute(ctx context.Context, call PreparedCall) (string, error) {
	id, err := operationID(ctx)
	if err != nil {
		return "", err
	}
	if call.Rejected != nil {
		data, err := json.Marshal(model.TravelToolResult{CallID: call.ID, Name: call.Name, Status: "rejected", Reason: call.Rejected.Message, Error: call.Rejected})
		return string(data), err
	}
	bounded, cancel := context.WithTimeout(ctx, nativeTimeout(r.cfg.Timeout))
	defer cancel()
	result := model.TravelToolResult{CallID: call.ID, Name: call.Name, Status: "ok"}
	switch call.Name {
	case "request_replan":
		if call.Replan == nil {
			return "", model.ErrInvalidInput
		}
		result.TripPlanIDs = call.Replan.TripPlanIDs
		result.Reason = call.Replan.Reason
	case "read":
		if call.Read == nil {
			return "", model.ErrInvalidInput
		}
		if r.cfg.History != nil {
			request := model.TravelHistoryRequest{Resource: call.Read.Resource, Offset: call.Read.Offset, Filters: model.TripConditions(call.Read.Filters)}
			if call.Read.SessionID != nil {
				request.ConversationID = *call.Read.SessionID
			}
			if call.Read.TripPlanID != nil {
				request.TripPlanID = *call.Read.TripPlanID
			}
			result.Data, err = r.cfg.History.Read(bounded, r.cfg.Execution, request)
		} else if call.Read.Resource == "trip" && call.Read.TripPlanID != nil {
			var view model.TripResult
			view, err = r.cfg.Trips.Inspect(bounded, r.cfg.Execution, *call.Read.TripPlanID)
			if err == nil {
				result.Data = mustJSON(NativeTripView(view))
			}
		} else {
			err = ruleError("READ_UNAVAILABLE", "历史读取尚未配置。", nil)
		}
	case "query":
		views := make([]json.RawMessage, len(call.Queries))
		group, qctx := errgroup.WithContext(bounded)
		group.SetLimit(3)
		for i, q := range call.Queries {
			group.Go(func() error {
				e, failure := r.cfg.Trips.Query(qctx, r.cfg.Execution, q)
				if failure != nil {
					views[i] = mustJSON(map[string]any{"status": "error", "domain": q.Domain, "query": NativeQueryParameters(q), "items": []any{}, "reason": "查询失败，供应商未返回可核实结果。"})
					if qctx.Err() != nil {
						return qctx.Err()
					}
					return nil
				}
				views[i] = mustJSON(NativeEvidenceView(e))
				return nil
			})
		}
		err = group.Wait()
		result.Data = mustJSON(views)
		for _, raw := range views {
			var row struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(raw, &row)
			if row.Status != "ok" {
				result.Status = "partial"
			}
		}
	case "write_memory":
		if call.Memory == nil {
			return "", model.ErrInvalidInput
		}
		if r.cfg.Memory == nil {
			err = ruleError("MEMORY_UNAVAILABLE", "偏好写入尚未配置。", nil)
		} else {
			command := model.TravelMemoryCommand{Operation: call.Memory.Operation}
			for _, update := range call.Memory.Updates {
				command.Updates = append(command.Updates, model.TravelMemoryUpdate{Op: update.Op, Text: update.Text, ID: update.ID, Source: "explicit"})
			}
			var memory model.TravelMemoryResult
			memory, err = r.cfg.Memory.Update(bounded, r.cfg.Execution, id, command)
			result.Data = mustJSON(memory)
			if len(memory.Rejected) > 0 {
				result.Status = "partial"
			}
		}
	case "query_policy":
		if call.Policy == nil {
			return "", model.ErrInvalidInput
		}
		policy := model.SourcePolicyContext{Status: "no_knowledge", Summary: "未取得适用政策资料，不能确认无限制。", Constraints: []model.SourcePolicyRule{}, Applicability: map[string]json.RawMessage{}, References: []model.PolicyFragment{}, MissingFields: []string{"policy_evidence"}, Conflicts: []json.RawMessage{}}
		if r.cfg.SourcePolicy != nil {
			request := model.TravelPolicyQuery{Context: model.TripConditions(call.Policy.Context)}
			if call.Policy.TripPlanID != nil {
				request.TripPlanID = *call.Policy.TripPlanID
			}
			policy, err = r.cfg.SourcePolicy.Query(bounded, r.cfg.Execution, request)
		}
		result.Data = mustJSON(policy)
		if policy.Status != "ok" {
			result.Status = "partial"
		}
	case "write_trip":
		if call.Trip == nil {
			return "", model.ErrInvalidInput
		}
		cmd := *call.Trip
		cmd.Context = call.Decision
		if cmd.Context == nil && r.cfg.Context != nil {
			decision := r.cfg.Context(bounded)
			cmd.Context = &decision
		}
		var saved model.TripResult
		saved, err = r.cfg.Trips.Apply(bounded, r.cfg.Execution, id, cmd)
		if err == nil {
			if saved.Receipt == nil || saved.Plan.ID == "" || saved.Receipt.PlanID != saved.Plan.ID || saved.Receipt.Revision != saved.Plan.Revision {
				err = ruleError("INVALID_SAVE_RECEIPT", "write result has no valid save receipt", nil)
			} else {
				if r.cfg.OnNativeTrip != nil {
					err = r.cfg.OnNativeTrip(bounded, call.ID, saved)
				}
				if err == nil {
					view := NativeTripView(saved)
					view["call_id"] = call.ID
					view["name"] = call.Name
					if call.Reply != nil {
						bound, bindingErr := tripsvc.BindReply(saved, model.TripReplySpec{Tasks: call.Reply.Tasks, Explanation: call.Reply.Explanation})
						if bindingErr != nil {
							err = bindingErr
						} else {
							view["rendered_answer"] = bound.Text
						}
					}
					if err == nil {
						data, e := json.Marshal(view)
						return string(data), e
					}
				}
			}
		}
	default:
		return "", model.ErrInvalidInput
	}
	if bounded.Err() != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		err = ruleError("TOOL_TIMEOUT", "工具查询或执行超时，结果尚未完成。", nil)
	}
	if err != nil {
		var rule *model.TravelRuleError
		if errors.As(err, &rule) {
			result.Status = "error"
			result.Reason = rule.Message
			result.Error = rule
		} else if errors.Is(err, model.ErrNotFound) {
			result.Status = "error"
			result.Error = &model.TravelRuleError{Code: "NOT_FOUND", Message: "请求的行程、修订或证据不可用。"}
		} else if errors.Is(err, model.ErrConflict) {
			result.Status = "error"
			result.Error = &model.TravelRuleError{Code: "REVISION_CONFLICT", Message: "行程或任务修订已变化，请读取当前版本后再修改。"}
		} else {
			return "", err
		}
	}
	data, err := json.Marshal(result)
	return string(data), err
}

func nativeTimeout(value time.Duration) time.Duration {
	if value <= 0 {
		return 30 * time.Second
	}
	return value
}

func NativeTripView(result model.TripResult) map[string]any {
	p := result.Plan
	status := "partial"
	if p.Status == "completed" || p.Validation.Status == "complete" && slices.Contains([]string{"select", "amend", "confirm"}, result.Operation) {
		status = "completed"
	}
	if p.StopReason == "needs_input" {
		status = "needs_input"
	}
	rows := []any{}
	reconstructed := []any{}
	for _, t := range p.Tasks {
		var draft any
		taskStatus := "pending"
		if t.Train != nil || t.Hotel != nil {
			taskStatus = "draft"
			draft = map[string]any{"task_id": t.ID, "task_revision": max(1, t.Revision), "train_selection": nil, "hotel_selection": nil}
			d := draft.(map[string]any)
			if t.Train != nil {
				d["train_selection"] = t.Train.Ref
			}
			if t.Hotel != nil {
				d["hotel_selection"] = t.Hotel.Ref
			}
		}
		rows = append(rows, map[string]any{"id": t.ID, "revision": max(t.Revision, 1), "status": taskStatus, "origin": t.Origin, "destination": t.Destination, "purpose": t.Purpose, "requires_hotel": t.RequiresHotel, "conditions": t.Conditions, "content": t.Activities, "condition_strengths": t.ConditionStrengths, "draft_plan": draft})
		facts := map[string]any{"task_id": t.ID, "train": nil, "hotel": nil, "schedule": map[string]any{"departure_at": nil, "arrival_at": nil, "check_in": t.CheckIn, "check_out": t.CheckOut}}
		if t.Train != nil {
			facts["train"] = nativeCandidate(t.Train.Candidate, "train")
			schedule := facts["schedule"].(map[string]any)
			schedule["departure_at"] = t.Train.Candidate.DepartureAt
			schedule["arrival_at"] = t.Train.Candidate.ArrivalAt
		}
		if t.Hotel != nil {
			facts["hotel"] = nativeCandidate(t.Hotel.Candidate, "hotel")
		}
		reconstructed = append(reconstructed, facts)
	}
	issues := result.Validation.Issues
	if issues == nil {
		issues = []model.TripIssue{}
	}
	view := map[string]any{"status": status, "operation": result.Operation, "trip_plan_id": p.ID, "revision": p.Revision, "tasks": rows, "validation": map[string]any{"valid": result.Validation.Status == "complete", "issues": issues, "reconstructed_tasks": reconstructed}, "candidates": []any{}, "pending_queries": []any{}, "stop_reason": p.StopReason, "reason": p.StopExplanation, "stage": "awaiting_selection", "stop_planning": slices.Contains([]string{"provider_capability_missing", "repair_disabled", "paused", "cancelled", "needs_input", "hard_constraint_conflict", "policy_unverified", "goal_not_satisfied"}, p.StopReason)}
	for _, q := range result.Queries {
		pending := view["pending_queries"].([]any)
		pending = append(pending, map[string]any{"task_id": q.TaskID, "item": map[string]any{"domain": q.Domain, "parameters": NativeQueryParameters(q)}, "reason": "awaiting_query"})
		view["pending_queries"] = pending
	}
	if status == "completed" {
		view["committed_revision"] = p.Revision
		view["stage"] = "completed"
	}
	return view
}

func NativeQueryParameters(q model.TravelQuery) map[string]any {
	p := map[string]any{}
	set := func(key, value string) {
		if value != "" {
			p[key] = value
		}
	}
	switch q.Domain {
	case "train":
		set("origin", q.Origin)
		set("destination", q.Destination)
		set("departure_date", q.Date)
		set("arrival_date", q.ArrivalDate)
		set("arrival_before", q.ArrivalBefore)
		p["passengers"] = max(q.Passengers, 1)
	case "hotel":
		set("city", q.City)
		set("check_in", q.CheckIn)
		set("check_out", q.CheckOut)
		set("keywords", q.Keywords)
		p["guests"] = max(q.Guests, 1)
	case "weather":
		set("city", q.City)
		set("date", q.Date)
	case "web":
		set("query", q.Keywords)
	}
	return p
}
func NativeEvidenceView(e model.TravelEvidence) map[string]any {
	view := tripsvc.CandidateView(e)
	items := []any{}
	for _, candidate := range view.Candidates {
		item := nativeCandidate(candidate, e.Query.Domain)
		if gaps := view.Unknown[candidate.ID]; len(gaps) > 0 {
			item["unverified_constraints"] = gaps
		}
		if e.Query.Domain == "hotel" {
			item["check_in"] = e.Query.CheckIn
			item["check_out"] = e.Query.CheckOut
		}
		items = append(items, item)
	}
	if e.Query.Domain == "weather" {
		for _, weather := range e.Weather {
			items = append(items, weather)
		}
	}
	return map[string]any{"status": view.Status, "data_quality": e.Status, "domain": e.Query.Domain, "query_id": e.ID, "result_revision": e.ResultRevision, "query": NativeQueryParameters(e.Query), "items": items, "candidate_total": view.Total, "excluded_candidate_count": view.Excluded, "view_offset": 0, "has_more": view.HasMore, "source": e.Source, "fetched_at": e.Source.FetchedAt, "is_demo": e.IsDemo, "message": e.Message}
}
func nativeCandidate(c model.TravelCandidate, domain string) map[string]any {
	price := json.RawMessage("null")
	if c.PriceFen != nil {
		price = []byte(fmt.Sprintf("%d.%02d", *c.PriceFen/100, *c.PriceFen%100))
	}
	out := map[string]any{"id": c.ID, "is_demo": c.IsDemo}
	if domain == "train" {
		out["train_number"] = c.Name
		out["origin"] = c.Origin
		out["destination"] = c.Destination
		out["departure_at"] = c.DepartureAt
		out["arrival_at"] = c.ArrivalAt
		if c.DepartureAt != nil {
			out["departure_time"] = c.DepartureAt.In(sourceBeijing).Format("15:04")
		}
		if c.ArrivalAt != nil {
			out["arrival_time"] = c.ArrivalAt.In(sourceBeijing).Format("15:04")
		}
		out["train_kind"] = canonicalViewKind(c.TrainKind)
		out["seat_class"] = c.SeatClass
		out["price_cny"] = price
		out["availability"] = c.Availability
	} else if domain == "hotel" {
		out["hotel_name"] = c.Name
		out["city"] = c.City
		out["address"] = c.Address
		out["kind"] = "hotel_place"
		if c.PriceFen != nil {
			out["kind"] = "hotel_quote"
		}
		out["stay_total_cny"] = price
	} else {
		out["name"] = c.Name
		out["url"] = c.URL
	}
	return out
}

var sourceBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

func canonicalViewKind(value string) string {
	switch value {
	case "G":
		return "high_speed"
	case "D":
		return "emu"
	default:
		return value
	}
}
