package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CommitTravelPreference(ctx context.Context, in model.TravelPreferenceCommit) (model.TravelPreference, error) {
	var result model.TravelPreference
	ref, cmd := in.Execution, in.Command
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return result, err
	}
	data, ok, err := loadTravelReceipt(ctx, tx, ref, in.OperationID, in.RequestHash, "preference")
	if err != nil {
		return result, err
	}
	if ok {
		if err = json.Unmarshal(data, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	if err = claimTravelAction(ctx, tx, ref, in.OperationID, in.RequestHash, "write_travel_preference"); err != nil {
		return result, err
	}
	var userID string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", ref.Scope.UserID).Scan(&userID); err != nil {
		return result, err
	}
	var origin string
	err = tx.QueryRow(ctx, `SELECT p.origin FROM travel_preference_revisions p JOIN agent_runs a ON a.id=p.source_run_id JOIN conversations c ON c.id=a.conversation_id
WHERE p.user_id=$1 AND p.preference_key=$2 AND a.context_eligible AND(a.id=$3 OR a.status='succeeded') AND c.deleted_at IS NULL ORDER BY p.revision DESC LIMIT 1`, ref.Scope.UserID, cmd.Key, ref.RunID).Scan(&origin)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if cmd.Origin == "inferred" && origin == "explicit" {
		return result, model.ErrConflict
	}
	var revision int64
	if err = tx.QueryRow(ctx, "SELECT COALESCE(MAX(revision),0)+1 FROM travel_preference_revisions WHERE user_id=$1 AND preference_key=$2", ref.Scope.UserID, cmd.Key).Scan(&revision); err != nil {
		return result, err
	}
	value, err := json.Marshal(cmd.Value)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel_preference_revisions(user_id,preference_key,revision,conversation_id,source_run_id,value,retracted,origin,operation_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, ref.Scope.UserID, cmd.Key, revision, ref.ConversationID, ref.RunID, value, cmd.Operation == "retract", cmd.Origin, in.OperationID)
	if err != nil {
		return result, err
	}
	result = model.TravelPreference{Key: cmd.Key, Value: cmd.Value, Revision: revision, Retracted: cmd.Operation == "retract", Origin: cmd.Origin, SourceRunID: ref.RunID}
	data, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel_operation_receipts(run_id,user_id,conversation_id,operation_id,kind,request_hash,result) VALUES($1,$2,$3,$4,'preference',$5,$6)`, ref.RunID, ref.Scope.UserID, ref.ConversationID, in.OperationID, in.RequestHash, data)
	if err != nil {
		return result, err
	}
	if err = finishTravelAction(ctx, tx, ref, in.OperationID, data); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) travelReadScope(ctx context.Context, scope model.Scope, currentRunID string) (model.TravelExecution, error) {
	run, err := r.GetRun(ctx, scope, currentRunID)
	if err != nil {
		return model.TravelExecution{}, err
	}
	return model.TravelExecution{Scope: scope, ConversationID: run.ConversationID, RunID: run.ID, Attempt: run.Attempt}, nil
}
func (r *Repository) ReadTravelPreferences(ctx context.Context, scope model.Scope, currentRunID string) ([]model.TravelPreference, error) {
	ref, err := r.travelReadScope(ctx, scope, currentRunID)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON(p.preference_key) p.preference_key,p.value,p.revision,p.retracted,p.origin,p.source_run_id,p.source_runs
FROM travel_preference_revisions p JOIN agent_runs a ON a.id=p.source_run_id JOIN conversations c ON c.id=a.conversation_id
WHERE p.user_id=$1 AND a.context_eligible AND(a.id=$2 OR a.status='succeeded') AND c.deleted_at IS NULL
AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(p.source_runs) source(id)
LEFT JOIN agent_runs src ON src.id=source.id LEFT JOIN conversations sc ON sc.id=src.conversation_id
WHERE src.id IS NULL OR src.user_id<>$1 OR NOT src.context_eligible OR(src.id<>$2 AND src.status<>'succeeded') OR sc.deleted_at IS NOT NULL)
ORDER BY p.preference_key,p.revision DESC`, scope.UserID, currentRunID)
	if err != nil {
		return nil, err
	}
	var result []model.TravelPreference
	for rows.Next() {
		var p model.TravelPreference
		var value, sources []byte
		if err = rows.Scan(&p.Key, &value, &p.Revision, &p.Retracted, &p.Origin, &p.SourceRunID, &sources); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(value, &p.Value); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(sources, &p.SourceRuns); err != nil { rows.Close(); return nil, err }
		result = append(result, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
