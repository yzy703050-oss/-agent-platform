package platformpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/platformrun"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func deniedAuthority(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && e.Code == "42501"
}

func (r *Repository) AdmitRun(ctx context.Context, scope model.Scope, in platformrun.SubmitInput) (runtimev1.RunView, error) {
	var result runtimev1.RunView
	if r.runtime == nil {
		return result, fmt.Errorf("entry runtime is not configured")
	}
	in, err := platformrun.PrepareSubmit(scope, in)
	if err != nil {
		return result, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// Reading candidate identity before authority locks is allowed; no business
	// lock is taken until the control/session/context/limit boundary is held.
	var kind string
	err = tx.QueryRow(ctx, "SELECT agent_type FROM platform.conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL", in.ConversationID, scope.UserID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, model.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	authority, err := (control.Client{}).LockAdmission(ctx, tx, scope.UserID, scope.SessionID, in.ConversationID)
	if deniedAuthority(err) {
		return result, model.ErrUnauthenticated
	}
	if err != nil {
		return result, err
	}
	if err = lockUserConversation(ctx, tx, scope.UserID, in.ConversationID); err != nil {
		return result, err
	}
	// An expiry may have happened while waiting on business locks.
	if _, err = (control.Client{}).LockAdmission(ctx, tx, scope.UserID, scope.SessionID, in.ConversationID); err != nil {
		if deniedAuthority(err) {
			err = model.ErrUnauthenticated
		}
		return result, err
	}
	var hash string
	err = tx.QueryRow(ctx, "SELECT id,status,deadline_at,version,request_hash FROM platform.agent_runs WHERE user_id=$1 AND conversation_id=$2 AND idempotency_key=$3", scope.UserID, in.ConversationID, in.IdempotencyKey).Scan(&result.ID, &result.Status, &result.DeadlineAt, &result.Version, &hash)
	if err == nil {
		if hash != in.RequestHash {
			return runtimev1.RunView{}, model.ErrIdempotencyConflict
		}
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM platform.agent_runs WHERE conversation_id=$1 AND status IN('accepted','running','waiting'))", in.ConversationID).Scan(&active); err != nil {
		return result, err
	}
	if active {
		return runtimev1.RunView{}, model.ErrRunConflict
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM platform.agent_runs WHERE user_id=$1 AND status IN('accepted','running','waiting')", scope.UserID).Scan(&count); err != nil {
		return result, err
	}
	if count >= authority.MaxActiveRuns {
		return runtimev1.RunView{}, model.ErrQuota
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform.agent_threads(id,user_id,conversation_id,agent_kind,thread_role,context_epoch) VALUES($1,$2,$1,$3,'entry',$4) ON CONFLICT DO NOTHING`, in.ConversationID, scope.UserID, kind, authority.ContextEpoch); err != nil {
		return result, err
	}
	var epoch int64
	if err = tx.QueryRow(ctx, "SELECT context_epoch FROM platform.agent_threads WHERE id=$1 AND user_id=$2 AND conversation_id=$1 FOR UPDATE", in.ConversationID, scope.UserID).Scan(&epoch); err != nil {
		return result, err
	}
	if epoch != authority.ContextEpoch {
		return runtimev1.RunView{}, model.ErrConflict
	}
	var admittedAt time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&admittedAt); err != nil {
		return result, err
	}
	result = runtimev1.RunView{ID: uuid.NewString(), Status: "accepted", Version: 1, DeadlineAt: admittedAt.Add(r.runtime.RunTimeout)}
	jobID := uuid.NewString()
	budget, _ := json.Marshal(struct {
		control.Reservation
		Execution any `json:"execution"`
	}{r.runtime.Budget, map[string]any{"limits": r.runtime.Snapshot(result.DeadlineAt), "fingerprint": r.runtime.ConfigFingerprint}})
	if _, err = tx.Exec(ctx, "SELECT control.register_run_guard($1,$2,$3,$4,$5,$6::jsonb)", result.ID, scope.UserID, in.ConversationID, scope.SessionID, result.DeadlineAt, budget); err != nil {
		if deniedAuthority(err) {
			err = model.ErrUnauthenticated
		}
		return runtimev1.RunView{}, err
	}
	targets := []string{"entry"}
	seen := map[string]bool{"entry": true}
	for _, target := range r.runtime.ScopeTargets {
		if !seen[target] {
			targets = append(targets, target)
			seen[target] = true
		}
	}
	execScope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: uuid.NewString(), OwnerService: "platform", SubjectID: scope.UserID, SourceRunID: result.ID, ConversationID: in.ConversationID, ContextEpoch: epoch, ExpiresAt: result.DeadlineAt,
		Capabilities: []string{"execute", "commit", "model", "tool", "control_rpc", "context", "memory", "delegate", "evidence"}, Targets: targets, SourceScope: json.RawMessage(`{}`)})
	if err != nil {
		return runtimev1.RunView{}, err
	}
	limits, _ := json.Marshal(r.runtime.Snapshot(result.DeadlineAt))
	if _, err = tx.Exec(ctx, `INSERT INTO platform.agent_runs(id,user_id,conversation_id,page_id,origin_session_id,root_job_id,idempotency_key,request_hash,status,kernel_version,config_fingerprint,root_context_epoch,deadline_at,limit_snapshot,created_at,updated_at)
VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,'accepted',$9,$10,$11,$12,$13::jsonb,$14,$14)`, result.ID, scope.UserID, in.ConversationID, scope.PageID, scope.SessionID, jobID, in.IdempotencyKey, in.RequestHash, r.runtime.KernelVersion, r.runtime.ConfigFingerprint, epoch, result.DeadlineAt, limits, admittedAt); err != nil {
		return runtimev1.RunView{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform.agent_jobs(id,run_id,user_id,conversation_id,thread_id,scope_id,scope_hash,status,deadline_at,not_before,created_at,updated_at) VALUES($1,$2,$3,$4,$4,$5,$6,'ready',$7,$8,$8,$8)`, jobID, result.ID, scope.UserID, in.ConversationID, execScope.ScopeID, execScope.ScopeHash, result.DeadlineAt, admittedAt); err != nil {
		return runtimev1.RunView{}, err
	}
	if _, err = appendEntryMessage(ctx, tx, entryRun{ID: result.ID, UserID: scope.UserID, ConversationID: in.ConversationID, ContextEpoch: epoch}, "user", in.Content, "user_request", nil); err != nil {
		return runtimev1.RunView{}, err
	}
	if err = appendEntryEvent(ctx, tx, result.ID, "run.accepted", map[string]any{"status": "accepted", "version": 1, "job_id": jobID, "deadline_at": result.DeadlineAt}); err != nil {
		return runtimev1.RunView{}, err
	}
	// These are newly inserted guard/scope rows, and all pre-existing guards
	// were locked before business rows. This final clock check introduces no
	// reverse lock dependency on another transaction's control records.
	if _, err = (control.Client{}).AssertEntryControl(ctx, tx, runtimev1.ExecRef{ScopeID: execScope.ScopeID, ScopeHash: execScope.ScopeHash}, "execute"); err != nil {
		return runtimev1.RunView{}, err
	}
	return result, tx.Commit(ctx)
}

func lockUserConversation(ctx context.Context, tx pgx.Tx, userID, conversationID string) error {
	var id string
	if err := tx.QueryRow(ctx, "SELECT id FROM platform.users WHERE id=$1 FOR UPDATE", userID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrNotFound
		}
		return err
	}
	err := tx.QueryRow(ctx, "SELECT id FROM platform.conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE", conversationID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

func appendEntryMessage(ctx context.Context, tx pgx.Tx, run entryRun, role, text, key string, refs []runtimev1.SourceRef) (string, error) {
	var ordinal int64
	if err := tx.QueryRow(ctx, "UPDATE platform.conversations SET next_message_ordinal=next_message_ordinal+1 WHERE id=$1 RETURNING next_message_ordinal-1", run.ConversationID).Scan(&ordinal); err != nil {
		return "", err
	}
	if refs == nil {
		refs = []runtimev1.SourceRef{}
	}
	sources, err := json.Marshal(refs)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO platform.messages(id,conversation_id,run_id,user_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_refs,source_key)
VALUES($1,$2,$3,$4,$2,$5,$6,$7,$8,$9,$10::jsonb,$8)`, id, run.ConversationID, run.ID, run.UserID, ordinal, role, text, key, run.ContextEpoch, sources)
	return id, err
}

func appendEntryEvent(ctx context.Context, tx pgx.Tx, runID, kind string, payload any) error {
	var seq, attempt int64
	if err := tx.QueryRow(ctx, "UPDATE platform.agent_runs SET next_event_seq=next_event_seq+1 WHERE id=$1 RETURNING next_event_seq-1,attempt", runID).Scan(&seq, &attempt); err != nil {
		return err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO platform.run_events(run_id,seq,attempt,type,payload) VALUES($1,$2,$3,$4,$5::jsonb)", runID, seq, attempt, kind, b)
	return err
}

func (r *Repository) FinishRun(ctx context.Context, ref runtimev1.ExecRef, in platformrun.FinishInput) error {
	if ref.OwnerKind != "job" || ref.OwnerID == "" || ref.Lease.WorkerID == "" || ref.Lease.Epoch < 1 || in.ExpectedRunVersion < 1 ||
		(in.Status != "succeeded" && in.Status != "failed" && in.Status != "interrupted") || !utf8.ValidString(in.FinalText) ||
		(in.Status == "succeeded" && (strings.TrimSpace(in.FinalText) == "" || in.Code != "")) || (in.Status != "succeeded" && strings.TrimSpace(in.Code) == "") {
		return model.ErrInvalidInput
	}
	meta, err := r.entryMetadata(ctx, ref.OwnerID)
	if err != nil {
		return err
	}
	if terminalRun(meta.Status) {
		return model.ErrConflict
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	auth, err := (control.Client{}).AssertEntryControl(ctx, tx, ref, "commit")
	if deniedAuthority(err) {
		return model.ErrConflict
	}
	if err != nil {
		return err
	}
	run, job, err := lockEntry(ctx, tx, meta.ID, ref.OwnerID, auth)
	if err != nil {
		return err
	}
	if run.Version != in.ExpectedRunVersion || run.Status != "running" || !job.matches(ref) {
		return model.ErrConflict
	}
	if err = assertEntryClock(ctx, tx, ref, "commit", job); err != nil {
		return err
	}
	if err = finishEntryRows(ctx, tx, run, in); err != nil {
		return err
	}
	// Storage itself can wait across an expiry. Keep the control guard active
	// until the last clock/lease check, then close it in this same transaction.
	if err = assertEntryClock(ctx, tx, ref, "commit", job); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT control.close_run_guard($1)", run.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func finishEntryRows(ctx context.Context, tx pgx.Tx, run entryRun, in platformrun.FinishInput) error {
	rows, err := tx.Query(ctx, "SELECT id FROM platform.agent_jobs WHERE run_id=$1 ORDER BY id FOR UPDATE", run.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	messageID, err := appendEntryMessage(ctx, tx, run, "assistant", in.FinalText, "final", in.PartialRefs)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE platform.agent_runs SET status=$2,error_code=$3,partial_output=$4,context_eligible=($2='succeeded'),version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND version=$5 AND status IN('accepted','running','waiting')`, run.ID, in.Status, in.Code, in.FinalText, run.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return model.ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE platform.agent_jobs SET status=$2,worker_id=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE run_id=$1 AND status IN('ready','running','waiting')", run.ID, in.Status); err != nil {
		return err
	}
	return appendEntryEvent(ctx, tx, run.ID, "run."+in.Status, map[string]any{"status": in.Status, "code": in.Code, "version": run.Version + 1, "message_id": messageID})
}

func terminalRun(status string) bool {
	return status != "accepted" && status != "running" && status != "waiting"
}

func (r *Repository) CloseInvalidRun(ctx context.Context, runID string) (bool, error) {
	var jobID string
	err := r.pool.QueryRow(ctx, "SELECT root_job_id FROM platform.agent_runs WHERE id=$1", runID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, model.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT control.close_invalid_run_guard($1)", runID).Scan(&raw); err != nil {
		return false, err
	}
	var a struct {
		UserID         string `json:"subject_id"`
		ConversationID string `json:"conversation_id"`
		Reason         string `json:"reason"`
	}
	if err = json.Unmarshal(raw, &a); err != nil {
		return false, err
	}
	if a.Reason == "" {
		return false, tx.Commit(ctx)
	}
	if err = lockUserConversation(ctx, tx, a.UserID, a.ConversationID); err != nil {
		return false, err
	}
	run, err := scanEntryRun(tx.QueryRow(ctx, entryRunSelect+" WHERE r.id=$1 FOR UPDATE", runID))
	if err != nil {
		return false, err
	}
	if terminalRun(run.Status) {
		return false, tx.Commit(ctx)
	}
	var id string
	if err = tx.QueryRow(ctx, "SELECT id FROM platform.agent_jobs WHERE id=$1 AND run_id=$2 FOR UPDATE", jobID, runID).Scan(&id); err != nil {
		return false, err
	}
	status := "failed"
	if a.Reason == "deadline_exceeded" {
		status = "timed_out"
	}
	text := run.PartialOutput
	if text == "" {
		text = "任务未能完成：" + a.Reason
	}
	if err = finishEntryRows(ctx, tx, run, platformrun.FinishInput{Status: status, Code: a.Reason, FinalText: text, ExpectedRunVersion: run.Version}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
