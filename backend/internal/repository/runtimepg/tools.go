package runtimepg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	"agent-platform/backend/internal/toolquery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) readCall(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, info runtimeports.Execution, key string) (toolquery.QueryState, error) {
	state := toolquery.QueryState{Execution: info}
	record := &state.Record
	var args, sources, refs, problem []byte
	var created time.Time
	err := tx.QueryRow(ctx, "SELECT id,call_key,tool_name,domain,provider,mode,input_hash,arguments,source_refs,status,COALESCE(coverage,'unavailable'),COALESCE(result_refs,'[]'),error,created_at,context_epoch,deadline_at FROM "+s.schema()+".tool_calls WHERE "+s.ownerColumn()+"=$1 AND call_key=$2 FOR UPDATE", ref.OwnerID, key).Scan(&record.ID, &record.CallKey, &record.ToolName, &record.Domain, &record.Provider, &record.Mode, &record.InputHash, &args, &sources, &record.Status, &record.Coverage, &refs, &problem, &created, &record.ContextEpoch, &state.DeadlineAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, model.ErrNotFound
	}
	if err != nil {
		return state, err
	}
	record.UserID, record.ConversationID, record.RunID, record.ThreadID, record.SchemaVersion = info.UserID, info.ConversationID, info.RunID, info.ThreadID, 1
	record.AgentKind, record.ThreadRole = "travel", "plan_work"
	if s.platform {
		record.ThreadRole = "entry"
		if err = tx.QueryRow(ctx, "SELECT agent_kind FROM platform.agent_threads WHERE id=$1", info.ThreadID).Scan(&record.AgentKind); err != nil {
			return state, err
		}
	}
	record.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	if record.ContextEpoch != info.ContextEpoch {
		return state, model.ErrConflict
	}
	for _, value := range []struct {
		b   []byte
		out any
	}{{args, &record.Arguments}, {sources, &record.SourceRefs}, {refs, &record.ResultRefs}} {
		if err = json.Unmarshal(value.b, value.out); err != nil {
			return state, err
		}
	}
	if len(problem) > 0 {
		if err = json.Unmarshal(problem, &record.Error); err != nil {
			return state, err
		}
	}
	if err = s.checkSources(ctx, tx, info, record.SourceRefs); err != nil {
		return state, err
	}
	return state, nil
}

func (s *Store) BeginQuery(ctx context.Context, ref runtimev1.ExecRef, in toolquery.QueryInput) (toolquery.QueryState, error) {
	var state toolquery.QueryState
	if ref.OwnerKind == "operation" {
		return state, model.ErrInvalidInput
	}
	hash, err := toolquery.Fingerprint(in)
	if err != nil {
		return state, err
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return state, err
	}
	defer tx.Rollback(ctx)
	info, err := s.authorize(ctx, tx, ref, "tool", in.Domain)
	if err != nil {
		return state, err
	}
	state, err = s.readCall(ctx, tx, ref, info, in.CallKey)
	if err == nil {
		if state.Record.InputHash != hash {
			return state, runtimeports.ErrInputConflict
		}
		return state, tx.Commit(ctx)
	}
	if !errors.Is(err, model.ErrNotFound) {
		return state, err
	}
	if err = s.checkSources(ctx, tx, info, in.SourceRefs); err != nil {
		return state, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return state, err
	}
	deadline := now.Add(info.Limits.ToolTimeout)
	for _, bound := range []time.Time{info.Limits.DeadlineAt, info.Scope.ExpiresAt} {
		if bound.Before(deadline) {
			deadline = bound
		}
	}
	if !deadline.After(now) {
		return state, model.ErrConflict
	}
	if in.SourceRefs == nil {
		in.SourceRefs = []runtimev1.SourceRef{}
	}
	sources, _ := json.Marshal(in.SourceRefs)
	_, err = tx.Exec(ctx, `INSERT INTO `+s.schema()+`.tool_calls(id,user_id,conversation_id,run_id,thread_id,context_epoch,`+s.ownerColumn()+`,call_key,tool_name,domain,provider,mode,schema_version,arguments,input_hash,source_refs,status,coverage,result_refs,lease_epoch,deadline_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,$13::jsonb,$14,$15::jsonb,'accepted','unavailable','[]',$16,$17)`, uuid.NewString(), info.UserID, info.ConversationID, info.RunID, info.ThreadID, info.ContextEpoch, ref.OwnerID, in.CallKey, "query_"+in.Domain, in.Domain, in.Provider, in.Mode, in.Arguments, hash, sources, ref.Lease.Epoch, deadline)
	if err != nil {
		return state, err
	}
	state, err = s.readCall(ctx, tx, ref, info, in.CallKey)
	if err != nil {
		return state, err
	}
	return state, tx.Commit(ctx)
}

func (s *Store) FindSaved(ctx context.Context, ref runtimev1.ExecRef, key string) (runtimev1.SharedToolEvidenceRecord, bool, error) {
	var result runtimev1.SharedToolEvidenceRecord
	var domain string
	err := s.pool.QueryRow(ctx, "SELECT domain FROM "+s.schema()+".tool_calls WHERE "+s.ownerColumn()+"=$1 AND call_key=$2", ref.OwnerID, key).Scan(&domain)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback(ctx)
	info, err := s.authorize(ctx, tx, ref, "tool", domain)
	if err != nil {
		return result, false, err
	}
	state, err := s.readCall(ctx, tx, ref, info, key)
	if err != nil {
		return result, false, err
	}
	if state.Record.Status != "succeeded" {
		return result, false, tx.Commit(ctx)
	}
	if len(state.Record.ResultRefs) != 1 {
		return result, false, model.ErrConflict
	}
	result, err = s.localEvidence(ctx, tx, info, state.Record.ResultRefs[0].EvidenceID, state.Record.ResultRefs[0].ResultRevision)
	if err != nil {
		return result, false, err
	}
	return result, true, tx.Commit(ctx)
}

func (s *Store) SaveQuery(ctx context.Context, ref runtimev1.ExecRef, callID, attemptID string, in toolquery.Result) (runtimev1.SharedToolEvidenceRecord, error) {
	var result runtimev1.SharedToolEvidenceRecord
	var domain, key string
	if in.Snapshot == nil || in.FetchedAt.IsZero() || (in.Coverage != "complete" && in.Coverage != "partial" && in.Coverage != "empty") {
		return result, model.ErrInvalidInput
	}
	if err := s.pool.QueryRow(ctx, "SELECT domain,call_key FROM "+s.schema()+".tool_calls WHERE id=$1 AND "+s.ownerColumn()+"=$2", callID, ref.OwnerID).Scan(&domain, &key); err != nil {
		return result, err
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// Authorization before resources, resource settlement before business locks.
	var data scopeContext
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT control.scope_call_context($1,$2,'tool',$3)", ref.ScopeID, ref.ScopeHash, domain).Scan(&raw); err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return result, err
	}
	a, err := s.originalAttempt(ctx, tx, ref, attemptID)
	if err != nil {
		return result, err
	}
	if a.Epoch != ref.Lease.Epoch || a.LogicalKey != "tool:"+key {
		return result, model.ErrConflict
	}
	id := uuid.NewString()
	refBody, _ := json.Marshal(map[string]any{"kind": "tool_evidence", "id": id, "revision": 1})
	if err = (control.Client{}).SettleAttempt(ctx, tx, control.Settlement{AttemptID: attemptID, Status: "succeeded", Usage: &control.Reservation{ToolAttempts: 1}, ResponseRef: refBody}); err != nil {
		return result, err
	}
	info, err := s.lockOwner(ctx, tx, ref, data.Scope)
	if err != nil {
		return result, err
	}
	state, err := s.readCall(ctx, tx, ref, info, key)
	if err != nil {
		return result, err
	}
	if state.Record.Status == "succeeded" {
		return result, model.ErrConflict
	}
	expires := info.Scope.ExpiresAt
	if in.ExpiresAt != nil && in.ExpiresAt.Before(expires) {
		expires = *in.ExpiresAt
	}
	if !expires.After(in.FetchedAt) {
		return result, toolquery.ErrExpired
	}
	expireText := expires.UTC().Format(time.RFC3339Nano)
	// Bind the immutable evidence to the actual accepted query, including for
	// remote readers that cannot join this service's private tool_calls table.
	inputArgs, err := json.Marshal(state.Record.Arguments)
	if err != nil {
		return result, err
	}
	queryContext, err := json.Marshal(evidence.QueryContext{Domain: domain, Mode: state.Record.Mode, Provider: state.Record.Provider, Arguments: inputArgs, InputHash: state.Record.InputHash, Coverage: in.Coverage})
	if err != nil {
		return result, err
	}
	snapshotCopy := map[string]json.RawMessage{}
	for key, value := range in.Snapshot {
		snapshotCopy[key] = value
	}
	snapshotCopy["query_context"] = queryContext
	result = runtimev1.SharedToolEvidenceRecord{ID: id, UserID: info.UserID, ConversationID: info.ConversationID, ToolCallRecordID: callID, Domain: domain, Provider: state.Record.Provider, SchemaVersion: 1, ResultRevision: 1, Mode: state.Record.Mode, Snapshot: snapshotCopy, SourceRefs: state.Record.SourceRefs, FetchedAt: in.FetchedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: &expireText}
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	var checked runtimev1.SharedToolEvidenceRecord
	if err = runtimeprotocol.DecodeStrict(body, "SharedToolEvidenceRecord", &checked); err != nil {
		return result, err
	}
	snapshot, _ := json.Marshal(result.Snapshot)
	sources, _ := json.Marshal(result.SourceRefs)
	if _, err = tx.Exec(ctx, `INSERT INTO `+s.schema()+`.tool_evidence(id,result_revision,user_id,conversation_id,tool_call_record_id,schema_version,domain,provider,mode,snapshot,source_refs,fetched_at,expires_at) VALUES($1,1,$2,$3,$4,1,$5,$6,$7,$8::jsonb,$9::jsonb,$10,$11)`, id, info.UserID, info.ConversationID, callID, domain, result.Provider, result.Mode, snapshot, sources, in.FetchedAt, expires); err != nil {
		return result, err
	}
	refs, _ := json.Marshal([]runtimev1.EvidenceIdentity{{EvidenceID: id, ResultRevision: 1}})
	if _, err = tx.Exec(ctx, "UPDATE "+s.schema()+".tool_calls SET status='succeeded',coverage=$2,result_refs=$3::jsonb,error=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", callID, in.Coverage, refs); err != nil {
		return result, err
	}
	if _, err = s.authorize(ctx, tx, ref, "tool", domain); err != nil {
		return result, err
	}
	// PostgreSQL timestamps have microsecond precision. Return the committed
	// representation so the first response hashes exactly like later exports.
	result, err = s.localEvidence(ctx, tx, info, id, 1)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) FailQuery(ctx context.Context, ref runtimev1.ExecRef, id string, problem *runtimev1.ErrorDetail) error {
	var domain string
	if err := s.pool.QueryRow(ctx, "SELECT domain FROM "+s.schema()+".tool_calls WHERE id=$1 AND "+s.ownerColumn()+"=$2", id, ref.OwnerID).Scan(&domain); err != nil {
		return err
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = s.authorize(ctx, tx, ref, "tool", domain); err != nil {
		return err
	}
	raw, err := json.Marshal(problem)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema()+".tool_calls SET status='failed',error=$2::jsonb,coverage='unavailable',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status<>'succeeded'", id, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
