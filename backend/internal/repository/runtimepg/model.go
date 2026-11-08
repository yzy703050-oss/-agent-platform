package runtimepg

import (
	"context"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeprotocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) modelContext(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef) (scopeContext, error) {
	var data scopeContext
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT control.model_call_context($1,$2)", ref.ScopeID, ref.ScopeHash).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &data)
	}
	return data, err
}

func (s *Store) FindModelOutput(ctx context.Context, ref runtimev1.ExecRef, key string) (json.RawMessage, bool, error) {
	tx, err := s.tx(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	data, err := s.modelContext(ctx, tx, ref)
	if err != nil {
		return nil, false, err
	}
	if _, err = s.lockOwner(ctx, tx, ref, data.Scope); err != nil {
		return nil, false, err
	}
	column, owner := "task_id", ref.OwnerID
	if s.platform {
		column = "run_id"
		owner = data.Scope.SourceRunID
		if ref.OwnerKind == "operation" {
			column = "operation_id"
			owner = ref.OwnerID
		}
	}
	var raw string
	err = tx.QueryRow(ctx, "SELECT content FROM "+s.schema()+".messages WHERE "+column+"=$1 AND thread_id=$2 AND source_key=$3 AND message_kind='model_output'", owner, ref.ThreadID, "model:"+key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, tx.Commit(ctx)
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(raw), true, tx.Commit(ctx)
}

func (s *Store) SaveModelOutput(ctx context.Context, ref runtimev1.ExecRef, attemptID, key string, output json.RawMessage) error {
	canonical, err := runtimeprotocol.CanonicalJSON(output)
	if err != nil {
		return err
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	data, err := s.modelContext(ctx, tx, ref)
	if err != nil {
		return err
	}
	info, err := s.lockOwner(ctx, tx, ref, data.Scope)
	if err != nil {
		return err
	}
	a, err := s.originalAttempt(ctx, tx, ref, attemptID)
	if err != nil {
		return err
	}
	if a.Epoch != ref.Lease.Epoch || a.LogicalKey != "model:"+key {
		return model.ErrConflict
	}
	column, owner := "task_id", ref.OwnerID
	if s.platform {
		column = "run_id"
		owner = info.RunID
		if ref.OwnerKind == "operation" {
			column = "operation_id"
			owner = ref.OwnerID
		}
	}
	var previous string
	err = tx.QueryRow(ctx, "SELECT content FROM "+s.schema()+".messages WHERE "+column+"=$1 AND thread_id=$2 AND source_key=$3", owner, ref.ThreadID, "model:"+key).Scan(&previous)
	if err == nil {
		if previous != string(canonical) {
			return model.ErrIdempotencyConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var ordinal int64
	if s.platform {
		if err = tx.QueryRow(ctx, "UPDATE platform.conversations SET next_message_ordinal=next_message_ordinal+1 WHERE id=$1 RETURNING next_message_ordinal-1", info.ConversationID).Scan(&ordinal); err != nil {
			return err
		}
		var run, operation any = info.RunID, nil
		if ref.OwnerKind == "operation" {
			run = nil
			operation = ref.OwnerID
		}
		_, err = tx.Exec(ctx, `INSERT INTO platform.messages(id,conversation_id,run_id,operation_id,user_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_key) VALUES($1,$2,$3,$4,$5,$6,$7,'assistant',$8,'model_output',$9,$10)`, uuid.NewString(), info.ConversationID, run, operation, info.UserID, info.ThreadID, ordinal, string(canonical), info.ContextEpoch, "model:"+key)
	} else {
		if err = tx.QueryRow(ctx, "UPDATE travel.threads SET next_message_ordinal=next_message_ordinal+1 WHERE id=$1 RETURNING next_message_ordinal-1", info.ThreadID).Scan(&ordinal); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO travel.messages(id,task_id,user_id,conversation_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_key) VALUES($1,$2,$3,$4,$5,$6,'assistant',$7,'model_output',$8,$9)`, uuid.NewString(), ref.OwnerID, info.UserID, info.ConversationID, info.ThreadID, ordinal, string(canonical), info.ContextEpoch, "model:"+key)
	}
	if err != nil {
		return err
	}
	if _, err = s.lockOwner(ctx, tx, ref, data.Scope); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
