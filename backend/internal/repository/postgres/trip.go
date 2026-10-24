package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *Repository) GetTrip(ctx context.Context, scope model.Scope, id string) (model.TripPlan, error) {
	var p model.TripPlan
	var data []byte
	err := r.pool.QueryRow(ctx, `SELECT v.snapshot FROM travel_plans p
JOIN travel_plan_revisions v ON v.plan_id=p.id AND v.revision=p.current_revision
JOIN conversations c ON c.id=p.conversation_id AND c.user_id=p.user_id
WHERE p.id=$1 AND p.user_id=$2 AND c.deleted_at IS NULL
AND EXISTS(SELECT 1 FROM sessions s WHERE s.id=$3 AND s.user_id=p.user_id AND s.revoked_at IS NULL AND s.expires_at>now())`, id, scope.UserID, scope.SessionID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, model.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	return p, err
}

func (r *Repository) FindTripReceipt(ctx context.Context, ref model.TravelExecution, id, hash string) (model.TripResult, bool, error) {
	var result model.TripResult
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecutionState(ctx, tx, ref, true); err != nil {
		return result, false, err
	}
	data, ok, err := loadTravelReceipt(ctx, tx, ref, id, hash, "trip")
	if err != nil {
		return result, false, err
	}
	if ok {
		if err = json.Unmarshal(data, &result); err != nil {
			return result, false, err
		}
	}
	return result, ok, tx.Commit(ctx)
}

func (r *Repository) CommitTrip(ctx context.Context, in model.TripCommit) (model.TripResult, error) {
	ref := in.Execution
	p := in.Result.Plan
	if p.ID == "" || p.UserID != ref.Scope.UserID || p.ConversationID != ref.ConversationID || p.SourceRunID != ref.RunID || in.ExpectedRevision < 0 || p.Revision != in.ExpectedRevision+1 {
		return model.TripResult{}, model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.TripResult{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTravelExecution(ctx, tx, ref); err != nil {
		return model.TripResult{}, err
	}
	data, ok, err := loadTravelReceipt(ctx, tx, ref, in.OperationID, in.RequestHash, "trip")
	if err != nil {
		return model.TripResult{}, err
	}
	if ok {
		var old model.TripResult
		err = json.Unmarshal(data, &old)
		if err != nil {
			return old, err
		}
		return old, tx.Commit(ctx)
	}
	if err = claimTravelAction(ctx, tx, ref, in.OperationID, in.RequestHash, "write_trip"); err != nil {
		return model.TripResult{}, err
	}
	if in.ExpectedRevision == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO travel_plans(id,user_id,conversation_id,source_run_id,current_revision) VALUES($1,$2,$3,$4,$5)`, p.ID, p.UserID, p.ConversationID, ref.RunID, p.Revision)
		if err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return model.TripResult{}, model.ErrConflict
			}
			return model.TripResult{}, err
		}
	} else {
		var revision int64
		err = tx.QueryRow(ctx, "SELECT current_revision FROM travel_plans WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE", p.ID, ref.Scope.UserID, ref.ConversationID).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.TripResult{}, model.ErrNotFound
		}
		if err != nil {
			return model.TripResult{}, err
		}
		if revision != in.ExpectedRevision {
			return model.TripResult{}, model.ErrConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE travel_plans SET current_revision=$2,updated_at=now() WHERE id=$1", p.ID, p.Revision); err != nil {
			return model.TripResult{}, err
		}
	}
	for _, expected := range in.Evidence {
		stored, err := loadTravelEvidence(ctx, tx, ref, model.TravelCandidateRef{QueryID: expected.ID, ResultRevision: expected.ResultRevision}, true)
		if err != nil {
			return model.TripResult{}, err
		}
		storedBytes, _ := json.Marshal(stored)
		expectedBytes, _ := json.Marshal(expected)
		if !bytes.Equal(storedBytes, expectedBytes) {
			return model.TripResult{}, model.ErrConflict
		}
	}
	data, err = json.Marshal(p)
	if err != nil {
		return model.TripResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO travel_plan_revisions(plan_id,user_id,conversation_id,revision,source_run_id,snapshot) VALUES($1,$2,$3,$4,$5,$6)`, p.ID, p.UserID, p.ConversationID, p.Revision, ref.RunID, data); err != nil {
		return model.TripResult{}, err
	}
	result := in.Result
	result.Receipt = &model.TravelReceipt{OperationID: in.OperationID, PlanID: p.ID, Revision: p.Revision, RequestHash: in.RequestHash}
	data, err = json.Marshal(result)
	if err != nil {
		return model.TripResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO travel_operation_receipts(run_id,user_id,conversation_id,operation_id,kind,request_hash,result) VALUES($1,$2,$3,$4,'trip',$5,$6)`, ref.RunID, ref.Scope.UserID, ref.ConversationID, in.OperationID, in.RequestHash, data); err != nil {
		return model.TripResult{}, err
	}
	if err = finishTravelAction(ctx, tx, ref, in.OperationID, data); err != nil {
		return model.TripResult{}, err
	}
	if p.SourceVersion > 0 {
		if err = enqueueTravelQueryJobs(ctx, tx, ref, in.OperationID, result.Queries); err != nil {
			return model.TripResult{}, err
		}
	}
	return result, tx.Commit(ctx)
}
