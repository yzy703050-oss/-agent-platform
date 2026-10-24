package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) GetAgentTrip(ctx context.Context, ref model.TravelExecution, id string) (model.TripPlan, error) {
	var p model.TripPlan
	var raw []byte
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return p, err
	}
	err = tx.QueryRow(ctx, `SELECT v.snapshot FROM travel_plans p JOIN travel_plan_revisions v ON v.plan_id=p.id AND v.revision=p.current_revision JOIN agent_runs a ON a.id=v.source_run_id JOIN conversations c ON c.id=p.conversation_id
WHERE p.id=$1 AND p.user_id=$2 AND c.deleted_at IS NULL AND a.context_eligible AND(a.id=$3 OR a.status='succeeded')`, id, ref.Scope.UserID, ref.RunID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, model.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (r *Repository) ReadTravelAnalysisTrips(ctx context.Context, ref model.TravelExecution) ([]model.TripPlan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT v.snapshot FROM travel_plans p JOIN travel_plan_revisions v ON v.plan_id=p.id AND v.revision=p.current_revision JOIN agent_runs a ON a.id=v.source_run_id JOIN conversations c ON c.id=p.conversation_id
WHERE p.user_id=$1 AND c.deleted_at IS NULL AND a.context_eligible AND(a.id=$2 OR a.status='succeeded') ORDER BY (p.conversation_id=$3) DESC,p.updated_at DESC,p.id`, ref.Scope.UserID, ref.RunID, ref.ConversationID)
	if err != nil {
		return nil, err
	}
	out := []model.TripPlan{}
	for rows.Next() {
		var raw []byte
		var p model.TripPlan
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			break
		}
		out = append(out, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func (r *Repository) ReadTravelHistory(ctx context.Context, ref model.TravelExecution, request model.TravelHistoryRequest) (json.RawMessage, error) {
	if request.Resource == "trip" {
		p, err := r.GetAgentTrip(ctx, ref, request.TripPlanID)
		if err != nil {
			return nil, err
		}
		p.QueryResults = nil
		return json.Marshal(p)
	}
	if request.Resource == "trip_summaries" {
		plans, err := r.ReadTravelAnalysisTrips(ctx, ref)
		if err != nil {
			return nil, err
		}
		summaries := []any{}
		for _, p := range plans {
			if !matchesHistoryFilters(p, request.Filters) {
				continue
			}
			tasks := []any{}
			for _, task := range p.Tasks {
				tasks = append(tasks, map[string]any{"id": task.ID, "origin": task.Origin, "destination": task.Destination, "purpose": task.Purpose, "conditions": task.Conditions})
			}
			summaries = append(summaries, map[string]any{"trip_plan_id": p.ID, "revision": p.Revision, "status": p.Status, "session_id": p.ConversationID, "tasks": tasks, "user_confirmed": p.UserConfirmed})
		}
		start := min(request.Offset, len(summaries))
		end := min(start+5, len(summaries))
		return json.Marshal(map[string]any{"summaries": summaries[start:end], "has_more": end < len(summaries), "needs_clarification": len(summaries) > 1})
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if request.Resource == "session_summaries" {
		var query string
		_ = json.Unmarshal(request.Filters["query"], &query)
		rows, err := tx.Query(ctx, `SELECT c.id,c.title,c.created_at,COALESCE((SELECT m.content FROM messages m JOIN agent_runs a ON a.id=m.run_id WHERE m.conversation_id=c.id AND m.role='user' AND a.context_eligible AND(a.id=$2 OR a.status='succeeded') ORDER BY m.ordinal DESC LIMIT 1),'') FROM conversations c
WHERE c.user_id=$1 AND c.deleted_at IS NULL AND c.agent_type='travel' AND EXISTS(SELECT 1 FROM agent_runs a WHERE a.conversation_id=c.id AND a.context_eligible AND(a.id=$2 OR a.status='succeeded')) ORDER BY c.created_at DESC,c.id`, ref.Scope.UserID, ref.RunID)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for rows.Next() {
			var id, title, last string
			var updated time.Time
			if err = rows.Scan(&id, &title, &updated, &last); err != nil {
				break
			}
			if strings.Contains(title+last, query) {
				out = append(out, map[string]any{"session_id": id, "title": title, "last_user_message": last, "updated_at": updated})
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		start := min(request.Offset, len(out))
		end := min(start+5, len(out))
		raw, err = json.Marshal(map[string]any{"summaries": out[start:end], "has_more": end < len(out)})
	} else if request.Resource == "session" {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL AND agent_type='travel')`, request.ConversationID, ref.Scope.UserID).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, model.ErrNotFound
		}
		rows, err := tx.Query(ctx, `SELECT m.role,m.content FROM messages m JOIN agent_runs a ON a.id=m.run_id WHERE m.user_id=$1 AND m.conversation_id=$2 AND m.role IN('user','assistant') AND a.context_eligible AND(a.id=$3 OR a.status='succeeded') ORDER BY m.created_at DESC,m.id DESC LIMIT 6 OFFSET $4`, ref.Scope.UserID, request.ConversationID, ref.RunID, request.Offset)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for rows.Next() {
			var role, content string
			if err = rows.Scan(&role, &content); err != nil {
				break
			}
			out = append(out, map[string]any{"role": role, "content": content})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		more := len(out) > 5
		out = out[:min(5, len(out))]
		slices.Reverse(out)
		raw, err = json.Marshal(map[string]any{"session_id": request.ConversationID, "entries": out, "summary": "", "has_more": more, "historical": true})
	} else {
		return nil, model.ErrInvalidInput
	}
	if err != nil {
		return nil, err
	}
	return raw, tx.Commit(ctx)
}

func matchesHistoryFilters(p model.TripPlan, filters model.TripConditions) bool {
	values := map[string]string{}
	for key, raw := range filters {
		var value string
		_ = json.Unmarshal(raw, &value)
		values[key] = value
	}
	if values["session_id"] != "" && values["session_id"] != p.ConversationID {
		return false
	}
	city, purpose := values["city"] == "", values["purpose"] == ""
	dates := []string{}
	for _, t := range p.Tasks {
		city = city || values["city"] == t.Origin || values["city"] == t.Destination
		purpose = purpose || values["purpose"] == t.Purpose
		for _, key := range []string{"departure_date", "arrival_date", "check_in", "check_out"} {
			var date string
			if json.Unmarshal(t.Conditions[key], &date) == nil && date != "" {
				dates = append(dates, date)
			}
		}
	}
	if !city || !purpose || values["date"] != "" && !slices.Contains(dates, values["date"]) {
		return false
	}
	slices.Sort(dates)
	return len(dates) == 0 || (values["start_date"] == "" || dates[len(dates)-1] >= values["start_date"]) && (values["end_date"] == "" || dates[0] <= values["end_date"])
}
