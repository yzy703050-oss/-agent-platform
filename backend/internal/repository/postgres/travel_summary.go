package postgres

import (
	"context"
	"encoding/json"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
)

func (r *Repository) SaveTravelSummary(ctx context.Context, ref model.TravelExecution, summary model.TravelSummary) (model.TravelSummary, error) {
	if summary.Content == "" || len(summary.Content) > 16000 || summary.Version <= 0 || len(summary.SourceRuns) == 0 {
		return model.TravelSummary{}, model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.TravelSummary{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return model.TravelSummary{}, err
	}
	seen := map[string]bool{}
	for _, id := range summary.SourceRuns {
		if seen[id] {
			return model.TravelSummary{}, model.ErrInvalidInput
		}
		seen[id] = true
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runs a JOIN conversations c ON c.id=a.conversation_id WHERE a.id=$1 AND a.user_id=$2 AND a.context_eligible AND(a.id=$3 OR a.status='succeeded') AND c.deleted_at IS NULL)`, id, ref.Scope.UserID, ref.RunID).Scan(&allowed)
		if err != nil {
			return model.TravelSummary{}, err
		}
		if !allowed {
			return model.TravelSummary{}, model.ErrConflict
		}
	}
	summary.ID = uuid.NewString()
	summary.SourceRunID = ref.RunID
	sources, err := json.Marshal(summary.SourceRuns)
	if err != nil {
		return model.TravelSummary{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel_memory_summaries(id,user_id,conversation_id,source_run_id,source_runs,version,content) VALUES($1,$2,$3,$4,$5,$6,$7)`, summary.ID, ref.Scope.UserID, ref.ConversationID, ref.RunID, sources, summary.Version, summary.Content)
	if err != nil {
		return model.TravelSummary{}, err
	}
	return summary, tx.Commit(ctx)
}

func (r *Repository) ListTravelSummaries(ctx context.Context, scope model.Scope, conversationID, currentRunID string) ([]model.TravelSummary, error) {
	ref, err := r.travelReadScope(ctx, scope, currentRunID)
	if err != nil {
		return nil, err
	}
	if ref.ConversationID != conversationID {
		return nil, model.ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT s.id,s.source_run_id,s.source_runs,s.version,s.content
FROM travel_memory_summaries s JOIN agent_runs owner_run ON owner_run.id=s.source_run_id
WHERE s.user_id=$1 AND s.conversation_id=$2 AND s.valid AND owner_run.context_eligible AND(owner_run.id=$3 OR owner_run.status='succeeded')
AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(s.source_runs) source(id)
 LEFT JOIN agent_runs a ON a.id=source.id LEFT JOIN conversations c ON c.id=a.conversation_id
 WHERE a.id IS NULL OR a.user_id<>$1 OR NOT a.context_eligible OR(a.id<>$3 AND a.status<>'succeeded') OR c.deleted_at IS NOT NULL)
ORDER BY s.created_at DESC,s.id DESC LIMIT 3`, scope.UserID, conversationID, currentRunID)
	if err != nil {
		return nil, err
	}
	var result []model.TravelSummary
	for rows.Next() {
		var s model.TravelSummary
		var sources []byte
		if err = rows.Scan(&s.ID, &s.SourceRunID, &sources, &s.Version, &s.Content); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(sources, &s.SourceRuns); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
