package postgres

import (
	"context"
	"encoding/json"
	"errors"

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
func (r *Repository) LoadTravelEvidence(ctx context.Context, ref model.TravelExecution, refs []model.TravelCandidateRef) ([]model.TravelEvidence, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
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
