// Package runtimepg shares fixed platform/travel storage mechanics. Fact tables
// remain service-local; no caller can supply a schema or SQL identifier.
package runtimepg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool     *pgxpool.Pool
	platform bool
}

func NewPlatform(pool *pgxpool.Pool) *Store { return &Store{pool: pool, platform: true} }
func NewTravel(pool *pgxpool.Pool) *Store   { return &Store{pool: pool} }
func (s *Store) schema() string {
	if s.platform {
		return "platform"
	}
	return "travel"
}
func (s *Store) ownerColumn() string {
	if s.platform {
		return "owner_job_id"
	}
	return "owner_task_id"
}
func (s *Store) tx(ctx context.Context) (pgx.Tx, error) {
	return s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
}

type scopeContext struct {
	Scope     control.AuthorizedScope `json:"scope"`
	Execution *struct {
		Limits      runtimev1.LimitSnapshot `json:"limits"`
		Fingerprint string                  `json:"fingerprint"`
	} `json:"execution"`
}

func (s *Store) authorize(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, cap, target string) (runtimeports.Execution, error) {
	var data scopeContext
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT control.scope_call_context($1,$2,$3,$4)", ref.ScopeID, ref.ScopeHash, cap, target).Scan(&raw); err != nil {
		return runtimeports.Execution{}, err
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return runtimeports.Execution{}, err
	}
	info, err := s.lockOwner(ctx, tx, ref, data.Scope)
	if err != nil {
		return info, err
	}
	if data.Execution != nil {
		info.Limits = data.Execution.Limits
		info.Fingerprint = data.Execution.Fingerprint
	} else if s.platform && ref.OwnerKind == "job" {
		limits, _ := json.Marshal(info.Limits)
		if _, err = tx.Exec(ctx, "SELECT control.attach_run_execution($1,$2,$3::jsonb,$4)", ref.ScopeID, ref.ScopeHash, limits, info.Fingerprint); err != nil {
			return info, err
		}
	} else {
		return info, fmt.Errorf("original execution limits unavailable")
	}
	if info.Limits.MaxModelAttempts < 0 || info.Limits.MaxToolAttempts < 0 || (cap == "model" && info.Limits.MaxModelAttempts < 1) || (cap == "tool" && info.Limits.MaxToolAttempts < 1) || info.Limits.MaxModelInputTokens < 1 || info.Limits.MaxModelOutputTokens < 1 || info.Limits.TokenUpperBound < 1 || info.Limits.ToolTimeout <= 0 || info.Fingerprint == "" {
		return info, fmt.Errorf("invalid original execution limits")
	}
	return info, nil
}

func (s *Store) lockOwner(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, auth control.AuthorizedScope) (runtimeports.Execution, error) {
	info := runtimeports.Execution{Scope: auth, Service: auth.OwnerService, UserID: auth.SubjectID, ConversationID: auth.ConversationID, RunID: auth.SourceRunID, ThreadID: ref.ThreadID, OperationID: auth.OperationID, ContextEpoch: auth.ContextEpoch}
	if ref.OwnerID == "" || ref.Lease.WorkerID == "" || ref.Lease.Epoch < 1 || ref.ThreadID == "" {
		return info, model.ErrConflict
	}
	var state, worker, scopeID, scopeHash, thread string
	var epoch, contextEpoch int64
	var expires pgtype.Timestamptz
	var deadline time.Time
	if s.platform {
		var id string
		if err := tx.QueryRow(ctx, "SELECT id FROM platform.users WHERE id=$1 FOR UPDATE", auth.SubjectID).Scan(&id); err != nil {
			return info, err
		}
		if err := tx.QueryRow(ctx, "SELECT id FROM platform.conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE", auth.ConversationID, auth.SubjectID).Scan(&id); err != nil {
			return info, err
		}
		if ref.OwnerKind == "job" && auth.SourceRunID != "" {
			var runState string
			var limits []byte
			if err := tx.QueryRow(ctx, "SELECT status,root_context_epoch,deadline_at,limit_snapshot,config_fingerprint FROM platform.agent_runs WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE", auth.SourceRunID, auth.SubjectID, auth.ConversationID).Scan(&runState, &contextEpoch, &deadline, &limits, &info.Fingerprint); err != nil {
				return info, err
			}
			if runState != "running" || contextEpoch != auth.ContextEpoch {
				return info, model.ErrConflict
			}
			if err := json.Unmarshal(limits, &info.Limits); err != nil {
				return info, err
			}
			if err := tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,''),lease_epoch,lease_expires_at,scope_id,scope_hash,thread_id FROM platform.agent_jobs WHERE id=$1 AND run_id=$2 AND user_id=$3 AND conversation_id=$4 FOR UPDATE`, ref.OwnerID, auth.SourceRunID, auth.SubjectID, auth.ConversationID).Scan(&state, &worker, &epoch, &expires, &scopeID, &scopeHash, &thread); err != nil {
				return info, err
			}
		} else if ref.OwnerKind == "operation" && auth.OperationID == ref.OwnerID && auth.SourceRunID == "" {
			if err := tx.QueryRow(ctx, "SELECT id FROM platform.agent_threads WHERE id=$1 AND user_id=$2 AND context_epoch=$3 FOR UPDATE", ref.ThreadID, auth.SubjectID, auth.ContextEpoch).Scan(&id); err != nil {
				return info, err
			}
			if err := tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,''),lease_epoch,lease_expires_at,thread_id,context_epoch FROM platform.memory_operations WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE`, ref.OwnerID, auth.SubjectID, auth.ConversationID).Scan(&state, &worker, &epoch, &expires, &thread, &contextEpoch); err != nil {
				return info, err
			}
			scopeID, scopeHash, deadline = auth.ScopeID, auth.ScopeHash, auth.ExpiresAt
			if contextEpoch != auth.ContextEpoch {
				return info, model.ErrConflict
			}
		} else {
			return info, model.ErrConflict
		}
	} else {
		if ref.OwnerKind != "task" || auth.SourceRunID == "" {
			return info, model.ErrConflict
		}
		var caller, delegation string
		if err := tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,''),lease_epoch,lease_expires_at,scope_id,scope_hash,COALESCE(thread_id,''),deadline_at,caller_id,delegation_id FROM travel.tasks WHERE id=$1 AND source_run_id=$2 AND subject_id=$3 AND conversation_id=$4 FOR UPDATE`, ref.OwnerID, auth.SourceRunID, auth.SubjectID, auth.ConversationID).Scan(&state, &worker, &epoch, &expires, &scopeID, &scopeHash, &thread, &deadline, &caller, &delegation); err != nil {
			return info, err
		}
		if caller != auth.IssuerService || delegation != auth.DelegationID {
			return info, model.ErrConflict
		}
		var current int64
		if err := tx.QueryRow(ctx, "SELECT last_main_context_epoch FROM travel.threads WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE", thread, auth.SubjectID, auth.ConversationID).Scan(&current); err != nil {
			return info, err
		}
		if current != auth.ContextEpoch {
			return info, model.ErrConflict
		}
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return info, err
	}
	if state != "running" || worker != ref.Lease.WorkerID || epoch != ref.Lease.Epoch || !expires.Valid || !expires.Time.After(now) || !deadline.After(now) || scopeID != ref.ScopeID || scopeHash != ref.ScopeHash || thread != ref.ThreadID || !auth.ExpiresAt.After(now) {
		return info, model.ErrConflict
	}
	return info, nil
}

func (s *Store) LoadCall(ctx context.Context, ref runtimev1.ExecRef, key, cap, target string) (runtimeports.CallState, error) {
	var state runtimeports.CallState
	tx, err := s.tx(ctx)
	if err != nil {
		return state, err
	}
	defer tx.Rollback(ctx)
	state.Execution, err = s.authorize(ctx, tx, ref, cap, target)
	if err != nil {
		return state, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT control.call_history($1,$2,$3)", ref.ScopeID, ref.ScopeHash, key).Scan(&raw); err != nil {
		return state, err
	}
	if err = json.Unmarshal(raw, &state.Attempts); err != nil {
		return state, err
	}
	return state, tx.Commit(ctx)
}

func (s *Store) originalAttempt(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, id string) (runtimeports.AttemptRecord, error) {
	var a runtimeports.AttemptRecord
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT control.own_attempt($1,$2,$3)", id, ref.ScopeID, ref.ScopeHash).Scan(&raw); err != nil {
		return a, err
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, err
	}
	owner := a.OwnerJobID
	if ref.OwnerKind == "operation" {
		owner = a.OwnerOperationID
	}
	if owner != ref.OwnerID {
		return a, model.ErrConflict
	}
	return a, nil
}

func (s *Store) Reserve(ctx context.Context, ref runtimev1.ExecRef, in control.AttemptInput) (control.Attempt, error) {
	var a control.Attempt
	tx, err := s.tx(ctx)
	if err != nil {
		return a, err
	}
	defer tx.Rollback(ctx)
	in.Ref = ref
	a, err = (control.Client{}).ReserveAttempt(ctx, tx, in)
	if err != nil {
		return a, err
	}
	if _, err = tx.Exec(ctx, "SELECT control.bind_attempt_input($1,$2)", a.AttemptID, in.InputHash); err != nil {
		return a, err
	}
	a.InputHash = in.InputHash
	if _, err = s.authorize(ctx, tx, ref, in.Capability, in.Target); err != nil {
		return a, err
	}
	if in.Capability == "tool" {
		if ref.OwnerKind == "operation" {
			return a, model.ErrInvalidInput
		}
		var deadline time.Time
		err = tx.QueryRow(ctx, "UPDATE "+s.schema()+".tool_calls SET status='running',retry_count=$3,lease_epoch=$4,started_at=COALESCE(started_at,clock_timestamp()),updated_at=clock_timestamp() WHERE "+s.ownerColumn()+"=$1 AND call_key=$2 AND status IN('accepted','running','unknown') AND deadline_at>clock_timestamp() RETURNING deadline_at", ref.OwnerID, trimPrefix(in.LogicalKey, "tool:"), a.AttemptNo-1, ref.Lease.Epoch).Scan(&deadline)
		if errors.Is(err, pgx.ErrNoRows) {
			err = model.ErrConflict
		}
		if err != nil {
			return a, err
		}
	}
	return a, tx.Commit(ctx)
}

func (s *Store) Settle(ctx context.Context, ref runtimev1.ExecRef, in control.Settlement) error {
	tx, err := s.tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := s.originalAttempt(ctx, tx, ref, in.AttemptID)
	if err != nil {
		return err
	}
	if a.Epoch != ref.Lease.Epoch {
		return model.ErrConflict
	}
	if err = (control.Client{}).SettleAttempt(ctx, tx, in); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ScheduleRetry(ctx context.Context, ref runtimev1.ExecRef, id string, at time.Time) (time.Time, error) {
	tx, err := s.tx(ctx)
	if err != nil {
		return at, err
	}
	defer tx.Rollback(ctx)
	a, err := s.originalAttempt(ctx, tx, ref, id)
	if err != nil {
		return at, err
	}
	cap, target := "model", ""
	if len(a.LogicalKey) >= 5 && a.LogicalKey[:5] == "tool:" {
		cap = "tool"
		if err = tx.QueryRow(ctx, "SELECT domain FROM "+s.schema()+".tool_calls WHERE "+s.ownerColumn()+"=$1 AND call_key=$2", ref.OwnerID, trimPrefix(a.LogicalKey, "tool:")).Scan(&target); err != nil {
			return at, err
		}
	}
	if target == "" { // Resource identity is not supplied by the model.
		var raw []byte
		if err = tx.QueryRow(ctx, "SELECT control.own_attempt($1,$2,$3)", id, ref.ScopeID, ref.ScopeHash).Scan(&raw); err != nil {
			return at, err
		}
		var wire struct {
			Keys []string `json:"resource_keys"`
		}
		if err = json.Unmarshal(raw, &wire); err != nil || len(wire.Keys) == 0 {
			return at, model.ErrInvalidInput
		}
		target = wire.Keys[0]
	}
	_, err = s.authorize(ctx, tx, ref, cap, target)
	if err != nil {
		return at, err
	}
	if err = tx.QueryRow(ctx, "SELECT control.schedule_call_retry($1,$2)", id, at).Scan(&at); err != nil {
		return at, err
	}
	if cap == "tool" {
		if _, err = tx.Exec(ctx, "UPDATE "+s.schema()+".tool_calls SET next_retry_at=$3,updated_at=clock_timestamp() WHERE "+s.ownerColumn()+"=$1 AND call_key=$2", ref.OwnerID, trimPrefix(a.LogicalKey, "tool:"), at); err != nil {
			return at, err
		}
	}
	return at, tx.Commit(ctx)
}

func trimPrefix(value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}
