package travelpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	"github.com/jackc/pgx/v5"
)

func planArguments(kind string, input any) (json.RawMessage, string, error) {
	raw, err := json.Marshal(map[string]any{"kind": kind, "input": input})
	if err != nil {
		return nil, "", err
	}
	raw, err = runtimeprotocol.CanonicalJSON(raw)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
func findPlanAction(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, owner, id, hash string) (v1.CommitResult, bool, error) {
	var result v1.CommitResult
	var previous, status, task string
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT request_hash,status,result,task_id FROM travel.actions WHERE run_id=$1 AND operation_id=$2 AND user_id=$3 AND conversation_id=$4", info.RunID, id, info.UserID, info.ConversationID).Scan(&previous, &status, &raw, &task)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if previous != hash || task != owner {
		return result, false, model.ErrIdempotencyConflict
	}
	if status != "succeeded" {
		return result, false, model.ErrConflict
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, false, err
	}
	return result, true, nil
}
func savePlanAction(ctx context.Context, tx pgx.Tx, ref v1.ExecRef, info runtimeports.Execution, kind string, args json.RawMessage, hash, reason string, result v1.CommitResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel.actions(run_id,operation_id,task_id,user_id,conversation_id,delegation_id,plan_id,kind,arguments,request_hash,stage,agent_path,input_refs,reason,status,lease_epoch,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,'commit','travelplan','[]',$11,'succeeded',$12,$13::jsonb)`, info.RunID, result.OperationID, ref.OwnerID, info.UserID, info.ConversationID, info.Scope.DelegationID, result.PlanID, kind, args, hash, reason, ref.Lease.Epoch, body)
	return err
}

func (r *Repository) ReplayPlanAction(ctx context.Context, ref v1.ExecRef, operation, kind string, input any) (v1.CommitResult, bool, error) {
	var result v1.CommitResult
	var found bool
	if !validPlanOperation(operation) || (kind != "create_plan" && kind != "apply_patch") {
		return result, false, model.ErrInvalidInput
	}
	_, hash, err := planArguments(kind, input)
	if err != nil {
		return result, false, err
	}
	err = r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		var err error
		result, found, err = findPlanAction(ctx, tx, info, ref.OwnerID, operation, hash)
		return err
	})
	return result, found, err
}
