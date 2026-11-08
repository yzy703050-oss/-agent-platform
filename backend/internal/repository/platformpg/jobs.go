package platformpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/platformrun"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type entryRun struct {
	ID, UserID, ConversationID, Status string
	Version, ContextEpoch              int64
	DeadlineAt                         time.Time
	KernelVersion, Fingerprint         string
	LimitsJSON                         []byte
	PartialOutput, RootJobID           string
}

const entryRunSelect = `SELECT r.id,r.user_id,r.conversation_id,r.status,r.version,r.root_context_epoch,r.deadline_at,r.kernel_version,r.config_fingerprint,r.limit_snapshot,r.partial_output,r.root_job_id FROM platform.agent_runs r`

func scanEntryRun(row pgx.Row) (entryRun, error) {
	var r entryRun
	err := row.Scan(&r.ID, &r.UserID, &r.ConversationID, &r.Status, &r.Version, &r.ContextEpoch, &r.DeadlineAt, &r.KernelVersion, &r.Fingerprint, &r.LimitsJSON, &r.PartialOutput, &r.RootJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = model.ErrNotFound
	}
	return r, err
}

func (r *Repository) entryMetadata(ctx context.Context, jobID string) (entryRun, error) {
	return scanEntryRun(r.pool.QueryRow(ctx, entryRunSelect+" JOIN platform.agent_jobs j ON j.run_id=r.id WHERE j.id=$1 AND r.root_job_id=j.id", jobID))
}

type entryJob struct {
	ID, RunID, ThreadID, ScopeID, ScopeHash, Status, WorkerID string
	Epoch, ScheduleSeq, CheckpointVersion                     int64
	RetryCount                                                int
	Expires                                                   pgtype.Timestamptz
	DeadlineAt, NotBefore                                     time.Time
}

func lockEntry(ctx context.Context, tx pgx.Tx, runID, jobID string, auth control.AuthorizedScope) (entryRun, entryJob, error) {
	var job entryJob
	if auth.SourceRunID != runID {
		return entryRun{}, job, model.ErrConflict
	}
	if err := lockUserConversation(ctx, tx, auth.SubjectID, auth.ConversationID); err != nil {
		return entryRun{}, job, err
	}
	run, err := scanEntryRun(tx.QueryRow(ctx, entryRunSelect+" WHERE r.id=$1 AND r.user_id=$2 AND r.conversation_id=$3 FOR UPDATE", runID, auth.SubjectID, auth.ConversationID))
	if err != nil {
		return run, job, err
	}
	err = tx.QueryRow(ctx, `SELECT id,run_id,thread_id,scope_id,scope_hash,status,COALESCE(worker_id,''),lease_epoch,schedule_seq,checkpoint_version,lease_expires_at,deadline_at,not_before,retry_count
FROM platform.agent_jobs WHERE id=$1 AND run_id=$2 AND user_id=$3 AND conversation_id=$4 FOR UPDATE`, jobID, runID, auth.SubjectID, auth.ConversationID).Scan(&job.ID, &job.RunID, &job.ThreadID, &job.ScopeID, &job.ScopeHash, &job.Status, &job.WorkerID, &job.Epoch, &job.ScheduleSeq, &job.CheckpointVersion, &job.Expires, &job.DeadlineAt, &job.NotBefore, &job.RetryCount)
	if errors.Is(err, pgx.ErrNoRows) {
		err = model.ErrConflict
	}
	if err != nil {
		return run, job, err
	}
	if run.RootJobID != job.ID || job.ScopeID != auth.ScopeID || job.ScopeHash != auth.ScopeHash || job.ThreadID != run.ConversationID || run.ContextEpoch != auth.ContextEpoch {
		return run, job, model.ErrConflict
	}
	return run, job, nil
}

func (j entryJob) matches(ref runtimev1.ExecRef) bool {
	return ref.OwnerKind == "job" && j.ID == ref.OwnerID && j.ThreadID == ref.ThreadID && j.ScopeID == ref.ScopeID && j.ScopeHash == ref.ScopeHash &&
		j.Status == "running" && j.WorkerID == ref.Lease.WorkerID && j.Epoch == ref.Lease.Epoch && j.Expires.Valid
}

func assertEntryClock(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, cap string, job entryJob) error {
	if _, err := (control.Client{}).AssertEntryControl(ctx, tx, ref, cap); err != nil {
		if deniedAuthority(err) {
			return model.ErrConflict
		}
		return err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	if !job.Expires.Valid || !job.Expires.Time.After(now) || !job.DeadlineAt.After(now) {
		return model.ErrConflict
	}
	return nil
}

func readEntryLimits(run entryRun) (runtimev1.LimitSnapshot, error) {
	var limits runtimev1.LimitSnapshot
	if err := json.Unmarshal(run.LimitsJSON, &limits); err != nil {
		return limits, err
	}
	if limits.MaxModelAttempts < 1 || limits.MaxToolAttempts < 1 || limits.MaxModelInputTokens < 1 || limits.MaxModelOutputTokens < 1 || limits.CostUpperMicros < 0 ||
		limits.MaxModelInputTokens > limits.TokenUpperBound || limits.MaxModelOutputTokens > limits.TokenUpperBound-limits.MaxModelInputTokens || limits.ToolTimeout <= 0 || !limits.DeadlineAt.Equal(run.DeadlineAt) {
		return limits, fmt.Errorf("invalid saved limit snapshot")
	}
	return limits, nil
}

func makeEntryLease(run entryRun, job entryJob) (runtimev1.JobLease, error) {
	limits, err := readEntryLimits(run)
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	return runtimev1.JobLease{Ref: runtimev1.ExecRef{ScopeID: job.ScopeID, ScopeHash: job.ScopeHash, OwnerKind: "job", OwnerID: job.ID, ThreadID: job.ThreadID, Lease: runtimev1.LeaseRef{WorkerID: job.WorkerID, Epoch: job.Epoch, ExpiresAt: job.Expires.Time}}, ScheduleSeq: job.ScheduleSeq, CheckpointVersion: job.CheckpointVersion, DeadlineAt: run.DeadlineAt, RunVersion: run.Version, Limits: limits}, nil
}

func (r *Repository) ClaimEntry(ctx context.Context, workerID string) (runtimev1.JobLease, bool, error) {
	if r.runtime == nil {
		return runtimev1.JobLease{}, false, fmt.Errorf("entry runtime is not configured")
	}
	if workerID == "" {
		return runtimev1.JobLease{}, false, model.ErrInvalidInput
	}
	rows, err := r.pool.Query(ctx, `SELECT j.id FROM platform.agent_jobs j JOIN platform.agent_runs r ON r.id=j.run_id AND r.root_job_id=j.id
WHERE r.status IN('accepted','running','waiting') AND ((j.status='ready' AND (j.not_before<=clock_timestamp() OR r.deadline_at<=clock_timestamp())) OR (j.status='running' AND j.lease_expires_at<=clock_timestamp())) ORDER BY j.not_before,j.id LIMIT 32`)
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return runtimev1.JobLease{}, false, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	for _, id := range ids {
		lease, ok, err := r.claimOneEntry(ctx, id, workerID)
		if err != nil {
			return lease, false, err
		}
		if ok {
			return lease, true, nil
		}
	}
	return runtimev1.JobLease{}, false, nil
}

func (r *Repository) claimOneEntry(ctx context.Context, jobID, worker string) (runtimev1.JobLease, bool, error) {
	meta, err := r.entryMetadata(ctx, jobID)
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	var ref runtimev1.ExecRef
	if err = r.pool.QueryRow(ctx, "SELECT scope_id,scope_hash FROM platform.agent_jobs WHERE id=$1", jobID).Scan(&ref.ScopeID, &ref.ScopeHash); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	defer tx.Rollback(ctx)
	auth, err := (control.Client{}).AssertEntryControl(ctx, tx, ref, "execute")
	if deniedAuthority(err) {
		tx.Rollback(ctx)
		_, err = r.CloseInvalidRun(ctx, meta.ID)
		return runtimev1.JobLease{}, false, err
	}
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	run, job, err := lockEntry(ctx, tx, meta.ID, jobID, auth)
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if terminalRun(run.Status) || (job.Status != "ready" && job.Status != "running") || (job.Status == "ready" && job.NotBefore.After(now)) || (job.Status == "running" && (!job.Expires.Valid || job.Expires.Time.After(now))) {
		return runtimev1.JobLease{}, false, nil
	}
	code, err := r.entryRecoveryCode(ctx, tx, run, job)
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if code != "" {
		if _, err = tx.Exec(ctx, "SELECT control.close_run_guard($1)", run.ID); err != nil {
			return runtimev1.JobLease{}, false, err
		}
		text := run.PartialOutput
		if text == "" {
			text = "执行现场无法安全恢复：" + code
		}
		status := "interrupted"
		if code == "retry_exhausted" {
			status = "failed"
		}
		if err = finishEntryRows(ctx, tx, run, platformrun.FinishInput{Status: status, Code: code, FinalText: text, ExpectedRunVersion: run.Version}); err != nil {
			return runtimev1.JobLease{}, false, err
		}
		return runtimev1.JobLease{}, false, tx.Commit(ctx)
	}
	// Refresh the authoritative clock after checkpoint hashing/lock waits.
	if _, err = (control.Client{}).AssertEntryControl(ctx, tx, ref, "execute"); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if !run.DeadlineAt.After(now) || !job.DeadlineAt.After(now) {
		return runtimev1.JobLease{}, false, model.ErrConflict
	}
	until := now.Add(r.runtime.LeaseDuration)
	for _, bound := range []time.Time{run.DeadlineAt, job.DeadlineAt, auth.ExpiresAt} {
		if bound.Before(until) {
			until = bound
		}
	}
	if job.Status == "running" {
		job.RetryCount++
	}
	job.Status, job.WorkerID, job.Epoch, job.Expires = "running", worker, job.Epoch+1, pgtype.Timestamptz{Time: until, Valid: true}
	if _, err = tx.Exec(ctx, "UPDATE platform.agent_jobs SET status='running',worker_id=$2,lease_epoch=$3,lease_expires_at=$4,updated_at=$5,retry_count=$6 WHERE id=$1", job.ID, worker, job.Epoch, until, now, job.RetryCount); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if _, err = tx.Exec(ctx, "UPDATE platform.agent_runs SET status='running',version=version+1,updated_at=$2 WHERE id=$1", run.ID, now); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	run.Status = "running"
	run.Version++
	kind := "run.running"
	if job.Epoch > 1 {
		kind = "run.recovered"
	}
	if err = appendEntryEvent(ctx, tx, run.ID, kind, map[string]any{"status": "running", "version": run.Version, "job_id": job.ID, "lease_epoch": job.Epoch}); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	lease, err := makeEntryLease(run, job)
	if err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if err = assertEntryClock(ctx, tx, lease.Ref, "execute", job); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return runtimev1.JobLease{}, false, err
	}
	return lease, true, nil
}

func (r *Repository) entryRecoveryCode(ctx context.Context, tx pgx.Tx, run entryRun, job entryJob) (string, error) {
	if _, err := readEntryLimits(run); err != nil {
		return "limit_snapshot_invalid", nil
	}
	if job.Status == "running" && job.RetryCount >= 3 {
		return "retry_exhausted", nil
	}
	if run.KernelVersion != r.runtime.KernelVersion || run.Fingerprint != r.runtime.ConfigFingerprint {
		return "checkpoint_incompatible", nil
	}
	if job.CheckpointVersion == 0 {
		if job.Epoch == 0 && job.Status == "ready" {
			return "", nil
		}
		return "checkpoint_missing", nil
	}
	var engine, version, fingerprint, hash string
	var format int
	var epoch, ownerEpoch int64
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT engine,engine_version,format_version,fingerprint,payload,payload_hash,context_epoch,lease_epoch FROM platform.checkpoints WHERE job_id=$1 AND checkpoint_version=$2 AND run_id=$3 AND thread_id=$4`, job.ID, job.CheckpointVersion, run.ID, job.ThreadID).Scan(&engine, &version, &format, &fingerprint, &payload, &hash, &epoch, &ownerEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return "checkpoint_missing", nil
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	if engine != "eino" || version != "v0.9.0-beta.1" || format != 1 || fingerprint != run.Fingerprint || epoch != run.ContextEpoch || ownerEpoch > job.Epoch || len(payload) == 0 || hash != hex.EncodeToString(sum[:]) {
		return "checkpoint_incompatible", nil
	}
	return "", nil
}

func (r *Repository) RenewEntry(ctx context.Context, lease runtimev1.JobLease) (runtimev1.JobLease, error) {
	if r.runtime == nil {
		return runtimev1.JobLease{}, fmt.Errorf("entry runtime is not configured")
	}
	ref := lease.Ref
	if ref.OwnerKind != "job" || ref.OwnerID == "" || ref.Lease.WorkerID == "" || ref.Lease.Epoch < 1 {
		return runtimev1.JobLease{}, model.ErrInvalidInput
	}
	meta, err := r.entryMetadata(ctx, ref.OwnerID)
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	defer tx.Rollback(ctx)
	auth, err := (control.Client{}).AssertEntryControl(ctx, tx, ref, "execute")
	if deniedAuthority(err) {
		return runtimev1.JobLease{}, model.ErrConflict
	}
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	run, job, err := lockEntry(ctx, tx, meta.ID, ref.OwnerID, auth)
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	if run.Status != "running" || !job.matches(ref) || job.ScheduleSeq != lease.ScheduleSeq {
		return runtimev1.JobLease{}, model.ErrConflict
	}
	if err = assertEntryClock(ctx, tx, ref, "execute", job); err != nil {
		return runtimev1.JobLease{}, err
	}
	var until time.Time
	err = tx.QueryRow(ctx, `UPDATE platform.agent_jobs SET lease_expires_at=least(clock_timestamp()+($2::bigint*interval '1 microsecond'),deadline_at,$3::timestamptz),updated_at=clock_timestamp() WHERE id=$1 AND lease_expires_at>clock_timestamp() AND deadline_at>clock_timestamp() RETURNING lease_expires_at`, job.ID, r.runtime.LeaseDuration.Microseconds(), auth.ExpiresAt).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimev1.JobLease{}, model.ErrConflict
	}
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	job.Expires = pgtype.Timestamptz{Time: until, Valid: true}
	result, err := makeEntryLease(run, job)
	if err != nil {
		return runtimev1.JobLease{}, err
	}
	if err = assertEntryClock(ctx, tx, result.Ref, "execute", job); err != nil {
		return runtimev1.JobLease{}, err
	}
	return result, tx.Commit(ctx)
}
