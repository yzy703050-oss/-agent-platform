package postgres

import (
	"context"
	"encoding/json"

	"agent-platform/backend/internal/model"
)

// Tool observations are journaled separately from business receipts, in the
// action's result reference. A receipt remains the authority for each mutation.
func (r *Repository) FindTravelToolResult(ctx context.Context, ref model.TravelExecution, id string) (string, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return "", false, err
	}
	var status string
	var data []byte
	if err = tx.QueryRow(ctx, "SELECT status,result_reference FROM travel_actions WHERE run_id=$1 AND user_id=$2 AND operation_id=$3", ref.RunID, ref.Scope.UserID, id).Scan(&status, &data); err != nil {
		return "", false, err
	}
	if status == "unknown" {
		return "", false, model.ErrConflict
	}
	var out struct {
		Output *string `json:"tool_observation"`
	}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &out); err != nil {
			return "", false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}
	if out.Output != nil && status == "succeeded" {
		return *out.Output, true, nil
	}
	return "", false, nil
}
func (r *Repository) SaveTravelToolResult(ctx context.Context, ref model.TravelExecution, id, output string) error {
	if len(output) > 1000000 || !json.Valid([]byte(output)) {
		return model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"tool_observation": output})
	tag, err := tx.Exec(ctx, `UPDATE travel_actions SET status='succeeded',result_reference=$4,attempt=$5,updated_at=now() WHERE run_id=$1 AND user_id=$2 AND operation_id=$3 AND status<>'unknown'`, ref.RunID, ref.Scope.UserID, id, data, ref.Attempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return model.ErrConflict
	}
	return tx.Commit(ctx)
}
