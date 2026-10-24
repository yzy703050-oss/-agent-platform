package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CommitTravelMemory(ctx context.Context, in model.TravelMemoryCommit) (model.TravelMemoryResult, error) {
	result := in.Result
	ref := in.Execution
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
	if err = claimTravelAction(ctx, tx, ref, in.OperationID, in.RequestHash, "write_memory"); err != nil {
		return result, err
	}
	var userID string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", ref.Scope.UserID).Scan(&userID); err != nil {
		return result, err
	}
	sources := slices.Clone(in.SourceRuns)
	if sources == nil {
		sources = []string{}
	}
	for _, id := range sources {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runs a JOIN conversations c ON c.id=a.conversation_id WHERE a.id=$1 AND a.user_id=$2 AND a.context_eligible AND(a.id=$3 OR a.status='succeeded') AND c.deleted_at IS NULL)`, id, ref.Scope.UserID, ref.RunID).Scan(&allowed)
		if err != nil {
			return result, err
		}
		if !allowed {
			return result, model.ErrConflict
		}
	}
	// Check the exact eligible snapshot used by Service, under the shared user lock.
	expectedVersions := map[string]int64{}
	for key, version := range in.ExpectedVersions {
		expectedVersions[key] = version
	}
	for _, cmd := range in.Changes {
		if _, ok := expectedVersions[cmd.Key]; !ok {
			expectedVersions[cmd.Key] = 0
		}
	}
	for key, expected := range expectedVersions {
		var current int64
		err = tx.QueryRow(ctx, `SELECT p.revision FROM travel_preference_revisions p JOIN agent_runs a ON a.id=p.source_run_id JOIN conversations c ON c.id=a.conversation_id
WHERE p.user_id=$1 AND p.preference_key=$2 AND a.context_eligible AND(a.id=$3 OR a.status='succeeded') AND c.deleted_at IS NULL
AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(p.source_runs) source(id) LEFT JOIN agent_runs src ON src.id=source.id LEFT JOIN conversations sc ON sc.id=src.conversation_id
WHERE src.id IS NULL OR src.user_id<>$1 OR NOT src.context_eligible OR(src.id<>$3 AND src.status<>'succeeded') OR sc.deleted_at IS NOT NULL)
ORDER BY p.revision DESC LIMIT 1`, ref.Scope.UserID, key, ref.RunID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return result, err
		}
		if current != expected {
			return result, model.ErrConflict
		}
	}
	seen := map[string]bool{}
	sourceBytes, _ := json.Marshal(sources)
	for _, cmd := range in.Changes {
		if seen[cmd.Key] || cmd.Key == "" || !slices.Contains([]string{"set", "retract"}, cmd.Operation) || !slices.Contains([]string{"explicit", "inferred"}, cmd.Origin) {
			return result, model.ErrInvalidInput
		}
		seen[cmd.Key] = true
		var revision int64
		if err = tx.QueryRow(ctx, "SELECT COALESCE(MAX(revision),0) FROM travel_preference_revisions WHERE user_id=$1 AND preference_key=$2", ref.Scope.UserID, cmd.Key).Scan(&revision); err != nil {
			return result, err
		}
		value, _ := json.Marshal(cmd.Value)
		if _, err = tx.Exec(ctx, `INSERT INTO travel_preference_revisions(user_id,preference_key,revision,conversation_id,source_run_id,value,retracted,origin,operation_id,source_runs) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, ref.Scope.UserID, cmd.Key, revision+1, ref.ConversationID, ref.RunID, value, cmd.Operation == "retract", cmd.Origin, in.OperationID, sourceBytes); err != nil {
			return result, err
		}
	}
	data, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO travel_operation_receipts(run_id,user_id,conversation_id,operation_id,kind,request_hash,result) VALUES($1,$2,$3,$4,'preference',$5,$6)`, ref.RunID, ref.Scope.UserID, ref.ConversationID, in.OperationID, in.RequestHash, data); err != nil {
		return result, err
	}
	if err = finishTravelAction(ctx, tx, ref, in.OperationID, data); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
