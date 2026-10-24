package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

func enqueueTravelQueryJobs(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, parent string, queries []model.TravelQuery) error {
	for index, q := range queries {
		raw, err := json.Marshal(q)
		if err != nil {
			return err
		}
		id := model.TravelQueryJobID(parent, index, q)
		_, err = tx.Exec(ctx, `INSERT INTO travel_query_jobs(id,run_id,user_id,conversation_id,parent_operation_id,plan_id,plan_revision,ordinal,query,status,attempt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'accepted',$10) ON CONFLICT(id) DO NOTHING`, id, ref.RunID, ref.Scope.UserID, ref.ConversationID, parent, q.PlanID, q.PlanRevision, index, raw, ref.Attempt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) BeginTravelQueryJob(ctx context.Context, ref model.TravelExecution, id string, q model.TravelQuery) (model.TravelEvidence, bool, error) {
	var result model.TravelEvidence
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return result, false, err
	}
	var status string
	var raw, stored []byte
	err = tx.QueryRow(ctx, `SELECT status,query,result FROM travel_query_jobs WHERE id=$1 AND run_id=$2 AND user_id=$3 AND conversation_id=$4 FOR UPDATE`, id, ref.RunID, ref.Scope.UserID, ref.ConversationID).Scan(&status, &raw, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, model.ErrNotFound
	}
	if err != nil {
		return result, false, err
	}
	var original model.TravelQuery
	if err = json.Unmarshal(raw, &original); err != nil {
		return result, false, err
	}
	expected, _ := json.Marshal(original)
	actual, _ := json.Marshal(q)
	if !bytes.Equal(expected, actual) {
		return result, false, model.ErrIdempotencyConflict
	}
	if status == "succeeded" {
		if err = json.Unmarshal(stored, &result); err != nil {
			return result, false, err
		}
		return result, true, tx.Commit(ctx)
	}
	if status != "accepted" {
		if _, err = tx.Exec(ctx, "UPDATE travel_query_jobs SET status='unknown',updated_at=now() WHERE id=$1", id); err != nil {
			return result, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, false, err
		}
		return result, false, model.ErrTravelOutcomeUnknown
	}
	if _, err = tx.Exec(ctx, "UPDATE travel_query_jobs SET status='running',attempt=$2,updated_at=now() WHERE id=$1", id, ref.Attempt); err != nil {
		return result, false, err
	}
	return result, false, tx.Commit(ctx)
}

func finishTravelQueryJob(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, id string, e model.TravelEvidence) (model.TravelEvidence, error) {
	var status, parent, planID string
	var revision, attempt int64
	var raw, stored []byte
	err := tx.QueryRow(ctx, `SELECT status,parent_operation_id,plan_id,plan_revision,attempt,query,result FROM travel_query_jobs WHERE id=$1 AND run_id=$2 AND user_id=$3 AND conversation_id=$4 FOR UPDATE`, id, ref.RunID, ref.Scope.UserID, ref.ConversationID).Scan(&status, &parent, &planID, &revision, &attempt, &raw, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, model.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if status == "succeeded" {
		var saved model.TravelEvidence
		err = json.Unmarshal(stored, &saved)
		return saved, err
	}
	if status != "running" || attempt != ref.Attempt {
		return e, model.ErrTravelOutcomeUnknown
	}
	var q model.TravelQuery
	if err = json.Unmarshal(raw, &q); err != nil {
		return e, err
	}
	if q.PlanID != e.Query.PlanID || q.TaskID != e.Query.TaskID || q.Domain != e.Query.Domain || q.TaskRevision != e.Query.TaskRevision {
		return e, model.ErrIdempotencyConflict
	}
	var planBytes []byte
	err = tx.QueryRow(ctx, `SELECT v.snapshot FROM travel_plans p JOIN travel_plan_revisions v ON v.plan_id=p.id AND v.revision=p.current_revision WHERE p.id=$1 AND p.user_id=$2 AND p.conversation_id=$3 AND p.current_revision=$4 FOR UPDATE OF p`, planID, ref.Scope.UserID, ref.ConversationID, revision).Scan(&planBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, model.ErrConflict
	}
	if err != nil {
		return e, err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if _, err = tx.Exec(ctx, "UPDATE travel_query_jobs SET status='succeeded',result=$2,updated_at=now() WHERE id=$1", id, data); err != nil {
		return e, err
	}
	var pending bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM travel_query_jobs WHERE run_id=$1 AND parent_operation_id=$2 AND status<>'succeeded')", ref.RunID, parent).Scan(&pending); err != nil {
		return e, err
	}
	if pending {
		return e, nil
	}
	// Attach the group's actual observations as one new immutable business
	// snapshot. Planning/validation decisions remain in Service, never here.
	var p model.TripPlan
	if err = json.Unmarshal(planBytes, &p); err != nil {
		return e, err
	}
	if p.QueryResults == nil {
		p.QueryResults = map[string]model.TravelEvidence{}
	}
	rows, err := tx.Query(ctx, "SELECT result FROM travel_query_jobs WHERE run_id=$1 AND parent_operation_id=$2 ORDER BY ordinal", ref.RunID, parent)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var data []byte
		var result model.TravelEvidence
		if err = rows.Scan(&data); err != nil {
			break
		}
		if err = json.Unmarshal(data, &result); err != nil {
			break
		}
		if result.ID != "" {
			for key, old := range p.QueryResults {
				if old.TaskID == result.TaskID && old.Query.Domain == result.Query.Domain {
					delete(p.QueryResults, key)
				}
			}
			p.QueryResults[result.ID] = result
			if !slices.Contains(p.QueryOrder, result.ID) {
				p.QueryOrder = append(p.QueryOrder, result.ID)
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return e, err
	}
	p.Revision++
	p.SourceRunID = ref.RunID
	data, err = json.Marshal(p)
	if err != nil {
		return e, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO travel_plan_revisions(plan_id,user_id,conversation_id,revision,source_run_id,snapshot) VALUES($1,$2,$3,$4,$5,$6)`, p.ID, ref.Scope.UserID, ref.ConversationID, p.Revision, ref.RunID, data); err != nil {
		return e, err
	}
	_, err = tx.Exec(ctx, "UPDATE travel_plans SET current_revision=$2,updated_at=now() WHERE id=$1", p.ID, p.Revision)
	return e, err
}

func (r *Repository) FinishTravelQueryJob(ctx context.Context, ref model.TravelExecution, id string, e model.TravelEvidence) (model.TravelEvidence, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return e, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return e, err
	}
	e, err = finishTravelQueryJob(ctx, tx, ref, id, e)
	if err != nil {
		return e, err
	}
	return e, tx.Commit(ctx)
}

func (r *Repository) ListTravelQueryJobs(ctx context.Context, ref model.TravelExecution) ([]model.TravelQueryJob, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	out, err := readTravelJobs(ctx, tx, ref, "")
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
func readTravelJobs(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, parent string) ([]model.TravelQueryJob, error) {
	rows, err := tx.Query(ctx, `SELECT id,parent_operation_id,query,result FROM travel_query_jobs WHERE run_id=$1 AND user_id=$2 AND($3='' OR parent_operation_id=$3) ORDER BY parent_operation_id,ordinal`, ref.RunID, ref.Scope.UserID, parent)
	if err != nil {
		return nil, err
	}
	out := []model.TravelQueryJob{}
	for rows.Next() {
		var job model.TravelQueryJob
		var query, result []byte
		if err = rows.Scan(&job.ID, &job.ParentCallID, &query, &result); err != nil {
			break
		}
		if err = json.Unmarshal(query, &job.Query); err != nil {
			break
		}
		if len(result) > 0 {
			if err = json.Unmarshal(result, &job.Result); err != nil {
				break
			}
		}
		out = append(out, job)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return out, err
}

func (r *Repository) EnqueueTravelRefresh(ctx context.Context, ref model.TravelExecution, parent string, plan model.TripRefreshPlan) ([]model.TravelQueryJob, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	old, err := readTravelJobs(ctx, tx, ref, parent)
	if err != nil {
		return nil, err
	}
	if len(old) > 0 {
		return old, tx.Commit(ctx)
	}
	if parent == "" || len(plan.Queries) == 0 || len(plan.Invalidated) == 0 {
		return nil, model.ErrInvalidInput
	}
	var data []byte
	err = tx.QueryRow(ctx, `SELECT v.snapshot FROM travel_plans p JOIN travel_plan_revisions v ON v.plan_id=p.id AND v.revision=p.current_revision WHERE p.id=$1 AND p.user_id=$2 AND p.conversation_id=$3 AND p.current_revision=$4 FOR UPDATE OF p`, plan.PlanID, ref.Scope.UserID, ref.ConversationID, plan.ExpectedRevision).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrConflict
	}
	if err != nil {
		return nil, err
	}
	var p model.TripPlan
	if err = json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	for _, expired := range plan.Invalidated {
		e, ok := p.QueryResults[expired.QueryID]
		if !ok || e.ResultRevision != expired.ResultRevision || e.ExpiresAt.IsZero() || e.ExpiresAt.After(time.Now()) {
			return nil, model.ErrConflict
		}
	}
	for _, q := range plan.Queries {
		if q.PlanID != plan.PlanID || q.PlanRevision != plan.ExpectedRevision || !q.Refresh {
			return nil, model.ErrInvalidInput
		}
	}
	if err = enqueueTravelQueryJobs(ctx, tx, ref, parent, plan.Queries); err != nil {
		return nil, err
	}
	out, err := readTravelJobs(ctx, tx, ref, parent)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
