package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

func loadTravelEvidence(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, selection model.TravelCandidateRef, lock bool) (model.TravelEvidence, error) {
	var evidence model.TravelEvidence
	var data []byte
	query := `SELECT e.snapshot FROM travel_query_evidence e JOIN agent_runs r ON r.id=e.source_run_id
WHERE e.id=$1 AND e.result_revision=$2 AND e.user_id=$3 AND e.conversation_id=$4 AND e.expires_at>now()
AND r.context_eligible AND (r.id=$5 OR r.status='succeeded')`
	if lock {
		query += " FOR SHARE OF e"
	}
	err := tx.QueryRow(ctx, query, selection.QueryID, selection.ResultRevision, ref.Scope.UserID, ref.ConversationID, ref.RunID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence, model.ErrNotFound
	}
	if err != nil {
		return evidence, err
	}
	err = json.Unmarshal(data, &evidence)
	return evidence, err
}

func (r *Repository) FindTravelQuery(ctx context.Context, ref model.TravelExecution, key string) (model.TravelEvidence, bool, error) {
	var evidence model.TravelEvidence
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return evidence, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, ref.QueryJobID != ""); err != nil {
		return evidence, false, err
	}
	var data []byte
	err = tx.QueryRow(ctx, `SELECT e.snapshot FROM travel_query_evidence e JOIN agent_runs a ON a.id=e.source_run_id
WHERE e.user_id=$1 AND e.conversation_id=$2 AND e.query_key=$3 AND e.expires_at>now() AND a.context_eligible AND(a.id=$4 OR a.status='succeeded')
ORDER BY e.fetched_at DESC,e.id DESC LIMIT 1`, ref.Scope.UserID, ref.ConversationID, key, ref.RunID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence, false, tx.Commit(ctx)
	}
	if err != nil {
		return evidence, false, err
	}
	if err = json.Unmarshal(data, &evidence); err != nil {
		return evidence, false, err
	}
	return evidence, true, tx.Commit(ctx)
}

func (r *Repository) SaveTravelQuery(ctx context.Context, ref model.TravelExecution, key string, e model.TravelEvidence) (model.TravelEvidence, error) {
	if e.ID == "" || e.ResultRevision <= 0 || key == "" || e.UserID != ref.Scope.UserID || e.ConversationID != ref.ConversationID || e.SourceRunID != ref.RunID || e.PlanID != e.Query.PlanID || e.TaskID != e.Query.TaskID || !e.ExpiresAt.After(e.Source.FetchedAt) || !e.ExpiresAt.After(time.Now()) {
		return model.TravelEvidence{}, model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.TravelEvidence{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, ref.QueryJobID != ""); err != nil {
		return model.TravelEvidence{}, err
	}
	var planID any
	if e.PlanID != "" {
		planID = e.PlanID
		var revision int64
		err = tx.QueryRow(ctx, "SELECT current_revision FROM travel_plans WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE", e.PlanID, ref.Scope.UserID, ref.ConversationID).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.TravelEvidence{}, model.ErrNotFound
		}
		if err != nil {
			return model.TravelEvidence{}, err
		}
		if revision != e.Query.PlanRevision {
			return model.TravelEvidence{}, model.ErrConflict
		}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return model.TravelEvidence{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel_query_evidence(id,result_revision,user_id,conversation_id,source_run_id,plan_id,task_id,query_key,fetched_at,expires_at,snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, e.ID, e.ResultRevision, e.UserID, e.ConversationID, e.SourceRunID, planID, e.TaskID, key, e.Source.FetchedAt, e.ExpiresAt, data)
	if err != nil {
		return model.TravelEvidence{}, err
	}
	if ref.QueryJobID != "" {
		if e, err = finishTravelQueryJob(ctx, tx, ref, ref.QueryJobID, e); err != nil {
			return e, err
		}
	}
	return e, tx.Commit(ctx)
}
func (r *Repository) LoadTravelEvidence(ctx context.Context, ref model.TravelExecution, refs []model.TravelCandidateRef) ([]model.TravelEvidence, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return nil, err
	}
	seen := map[model.TravelCandidateRef]bool{}
	var result []model.TravelEvidence
	for _, selection := range refs {
		if seen[selection] {
			continue
		}
		seen[selection] = true
		evidence, err := loadTravelEvidence(ctx, tx, ref, selection, false)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, tx.Commit(ctx)
}
