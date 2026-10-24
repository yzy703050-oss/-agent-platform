package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
)

// All business writes use the platform's conversation -> run -> resource lock order.
func lockTravelExecution(ctx context.Context, tx pgx.Tx, ref model.TravelExecution) error {
	return lockTravelExecutionState(ctx, tx, ref, false)
}

// During safe-point stopping only the already in-flight model's decision may
// be recorded. Business mutations still require strictly running status.
func lockTravelExecutionState(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, journal bool) error {
	if ref.Scope.UserID == "" || ref.Scope.SessionID == "" || ref.RunID == "" || ref.ConversationID == "" {
		return model.ErrInvalidInput
	}
	run, err := lockRun(ctx, tx, ref.Scope, ref.RunID)
	if err != nil {
		return err
	}
	if run.ConversationID != ref.ConversationID || run.Attempt != ref.Attempt || (run.Status != "running" && !(journal && run.Status == "pausing")) {
		return model.ErrConflict
	}
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now())
 AND EXISTS(SELECT 1 FROM conversations WHERE id=$3 AND user_id=$2 AND agent_type='travel' AND deleted_at IS NULL)`, ref.Scope.SessionID, ref.Scope.UserID, ref.ConversationID).Scan(&authorized)
	if err != nil {
		return err
	}
	if !authorized {
		return model.ErrUnauthenticated
	}
	return nil
}

func loadTravelReceipt(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, id, hash, kind string) ([]byte, bool, error) {
	var storedHash, storedKind string
	var data []byte
	err := tx.QueryRow(ctx, "SELECT request_hash,kind,result FROM travel_operation_receipts WHERE user_id=$1 AND run_id=$2 AND operation_id=$3", ref.Scope.UserID, ref.RunID, id).Scan(&storedHash, &storedKind, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if storedHash != hash || storedKind != kind {
		return nil, false, model.ErrIdempotencyConflict
	}
	return data, true, nil
}

func claimTravelAction(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, id, hash, kind string) error {
	if id == "" || hash == "" {
		return model.ErrInvalidInput
	}
	_, err := tx.Exec(ctx, `INSERT INTO travel_actions(run_id,user_id,conversation_id,operation_id,kind,status,attempt,request_hash)
VALUES($1,$2,$3,$4,$5,'accepted',$6,$7) ON CONFLICT(run_id,operation_id) DO NOTHING`, ref.RunID, ref.Scope.UserID, ref.ConversationID, id, kind, ref.Attempt, hash)
	if err != nil {
		return err
	}
	var storedHash, storedKind, status string
	err = tx.QueryRow(ctx, "SELECT request_hash,kind,status FROM travel_actions WHERE run_id=$1 AND operation_id=$2 FOR UPDATE", ref.RunID, id).Scan(&storedHash, &storedKind, &status)
	if err != nil {
		return err
	}
	if storedKind != kind || (storedHash != "" && storedHash != hash) {
		return model.ErrIdempotencyConflict
	}
	if status == "unknown" {
		return model.ErrTravelOutcomeUnknown
	}
	if status == "succeeded" {
		return model.ErrConflict
	}
	_, err = tx.Exec(ctx, "UPDATE travel_actions SET request_hash=$3,attempt=$4,status='running',updated_at=now() WHERE run_id=$1 AND operation_id=$2", ref.RunID, id, hash, ref.Attempt)
	return err
}

func finishTravelAction(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, id string, result []byte) error {
	_, err := tx.Exec(ctx, "UPDATE travel_actions SET status='succeeded',result_reference=$3,updated_at=now() WHERE run_id=$1 AND operation_id=$2 AND attempt=$4", ref.RunID, id, result, ref.Attempt)
	return err
}

func loadTravelDecision(ctx context.Context, tx pgx.Tx, ref model.TravelExecution, key string) (model.TravelDecision, bool, error) {
	var d model.TravelDecision
	var data []byte
	err := tx.QueryRow(ctx, "SELECT snapshot FROM travel_decisions WHERE run_id=$1 AND user_id=$2 AND decision_key=$3", ref.RunID, ref.Scope.UserID, key).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	err = json.Unmarshal(data, &d)
	return d, err == nil, err
}

func (r *Repository) FindTravelDecision(ctx context.Context, ref model.TravelExecution, key string) (model.TravelDecision, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.TravelDecision{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return model.TravelDecision{}, false, err
	}
	d, ok, err := loadTravelDecision(ctx, tx, ref, key)
	if err != nil {
		return d, false, err
	}
	return d, ok, tx.Commit(ctx)
}

func (r *Repository) AcceptTravelDecision(ctx context.Context, ref model.TravelExecution, d model.TravelDecision) (model.TravelDecision, error) {
	if d.RunID != ref.RunID || d.DecisionKey == "" || d.RequestHash == "" || d.Stage == "" || d.Response.Role != "assistant" {
		return model.TravelDecision{}, model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.TravelDecision{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return model.TravelDecision{}, err
	}
	old, ok, err := loadTravelDecision(ctx, tx, ref, d.DecisionKey)
	if err != nil {
		return old, err
	}
	if ok {
		if old.RequestHash != d.RequestHash {
			return model.TravelDecision{}, model.ErrIdempotencyConflict
		}
		return old, tx.Commit(ctx)
	}
	d.OperationIDs = map[string]string{}
	for _, call := range d.Response.ToolCalls {
		if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) || d.OperationIDs[call.ID] != "" {
			return model.TravelDecision{}, model.ErrInvalidInput
		}
		parts, _ := json.Marshal([]string{ref.RunID, d.DecisionKey, call.ID})
		digest := sha256.Sum256(parts)
		id := hex.EncodeToString(digest[:])
		d.OperationIDs[call.ID] = id
		if _, err = tx.Exec(ctx, `INSERT INTO travel_actions(run_id,user_id,conversation_id,operation_id,kind,arguments,status,attempt)
VALUES($1,$2,$3,$4,$5,$6,'accepted',$7)`, ref.RunID, ref.Scope.UserID, ref.ConversationID, id, call.Name, []byte(call.Arguments), ref.Attempt); err != nil {
			return model.TravelDecision{}, err
		}
	}
	data, err := json.Marshal(d)
	if err != nil {
		return model.TravelDecision{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel_decisions(run_id,user_id,conversation_id,decision_key,stage,request_hash,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7)`, ref.RunID, ref.Scope.UserID, ref.ConversationID, d.DecisionKey, d.Stage, d.RequestHash, data)
	if err != nil {
		return model.TravelDecision{}, err
	}
	return d, tx.Commit(ctx)
}
