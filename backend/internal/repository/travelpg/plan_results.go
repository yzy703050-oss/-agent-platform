package travelpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	"agent-platform/backend/internal/service/travelplan"
	"github.com/jackc/pgx/v5"
)

func equalJSON(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	if e1 != nil || e2 != nil {
		return false
	}
	x, e1 = runtimeprotocol.CanonicalJSON(x)
	y, e2 = runtimeprotocol.CanonicalJSON(y)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

func (r *Repository) validateSelections(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, snapshot v1.PlanSnapshot, patch v1.PlanPatch) error {
	explicit := map[string]bool{}
	for _, op := range patch.Operations {
		if op.Op == "set_selection" {
			var selection v1.Selection
			if json.Unmarshal(op.Value.Value, &selection) != nil {
				return model.ErrInvalidInput
			}
			explicit[selection.SelectionID] = true
		}
	}
	for _, selection := range snapshot.Selections {
		stored, _, err := r.EvidenceInTransaction(ctx, tx, info, selection.Evidence.EvidenceID, selection.Evidence.ResultRevision)
		if err != nil {
			return err
		}
		var candidates []v1.Candidate
		if err = json.Unmarshal(stored.Record.Snapshot["candidates"], &candidates); err != nil {
			return err
		}
		found := false
		for _, candidate := range candidates {
			if candidate.CandidateID == selection.Evidence.CandidateID {
				found = true
			}
		}
		if !found {
			return model.ErrConflict
		}
		if explicit[selection.SelectionID] {
			var result []byte
			err = tx.QueryRow(ctx, `SELECT e.result FROM travel.execution_steps e WHERE e.plan_id=$1 AND e.task_id=$2 AND e.task_revision=$3 AND e.item_id=$4 AND e.evidence_id=$5 AND e.result_revision=$6 AND e.status='succeeded' AND e.generation=(SELECT max(x.generation) FROM travel.execution_steps x WHERE x.plan_id=e.plan_id AND x.task_id=e.task_id AND x.task_revision=e.task_revision AND x.item_id=e.item_id)`, snapshot.PlanID, selection.TaskID, selection.TaskRevision, selection.ItemID, selection.Evidence.EvidenceID, selection.Evidence.ResultRevision).Scan(&result)
			if errors.Is(err, pgx.ErrNoRows) {
				return model.ErrConflict
			}
			if err != nil {
				return err
			}
			var accepted v1.ExecutionResult
			if err = runtimeprotocol.DecodeStrict(result, "ExecutionResult", &accepted); err != nil {
				return err
			}
			exists := false
			for _, candidate := range accepted.Candidates {
				if candidate.CandidateID == selection.Evidence.CandidateID {
					exists = true
				}
			}
			if !exists {
				return model.ErrConflict
			}
		}
	}
	return nil
}

func (r *Repository) AcceptPlanQuery(ctx context.Context, ref v1.ExecRef, request v1.ExecutionRequest, supplied evidence.View) (v1.ExecutionResult, error) {
	var result v1.ExecutionResult
	raw, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	var checked v1.ExecutionRequest
	if err = runtimeprotocol.DecodeStrict(raw, "ExecutionRequest", &checked); err != nil {
		return result, err
	}
	request = checked
	err = r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		if request.Scope.UserID != info.UserID || request.Scope.MainThreadID != info.ConversationID || request.Scope.RunID != info.RunID || request.Scope.OperationID != request.ExecutionStepID || request.Scope.ToolCallID != "exec:"+request.ExecutionStepID {
			return model.ErrConflict
		}
		deadline, err := time.Parse(time.RFC3339Nano, request.Scope.Deadline)
		if err != nil || deadline.After(info.Scope.ExpiresAt) {
			return model.ErrConflict
		}
		var now time.Time
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		if !deadline.After(now) {
			return context.DeadlineExceeded
		}
		snapshot, err := loadPlanSnapshot(ctx, tx, info, request.PlanID, true)
		if err != nil {
			return err
		}
		current := false
		var task v1.TaskDefinition
		for _, version := range snapshot.TaskVersions {
			if version.TaskID == request.TaskID && version.TaskRevision == request.TaskRevision {
				current = true
			}
		}
		for _, t := range snapshot.Definition.Tasks {
			if t.TaskID == request.TaskID {
				task = t
			}
		}
		if !current {
			result = travelplan.StaleResult(request)
			return nil
		}
		var taskID, item, domain, status, hash, owner string
		var revision, generation, planRevision int64
		var storedInput, storedResult []byte
		var storedEvidence *string
		var storedEvidenceRevision *int64
		err = tx.QueryRow(ctx, `SELECT task_id,item_id,domain,task_revision,generation,plan_revision,status,input_hash,input,owner_task_id,evidence_id,result_revision,result FROM travel.execution_steps WHERE id=$1 AND plan_id=$2 AND user_id=$3 AND conversation_id=$4 FOR UPDATE`, request.ExecutionStepID, request.PlanID, info.UserID, info.ConversationID).Scan(&taskID, &item, &domain, &revision, &generation, &planRevision, &status, &hash, &storedInput, &owner, &storedEvidence, &storedEvidenceRevision, &storedResult)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrConflict
		}
		if err != nil {
			return err
		}
		var latest int64
		if err = tx.QueryRow(ctx, "SELECT max(generation) FROM travel.execution_steps WHERE plan_id=$1 AND task_id=$2 AND task_revision=$3 AND item_id=$4", request.PlanID, request.TaskID, request.TaskRevision, request.ItemID).Scan(&latest); err != nil {
			return err
		}
		if generation != request.Generation || generation != latest || status == "stale" {
			result = travelplan.StaleResult(request)
			return nil
		}
		if taskID != request.TaskID || item != request.ItemID || domain != request.Domain || revision != request.TaskRevision || planRevision != request.PlanRevision || owner != ref.OwnerID || status == "blocked" {
			return model.ErrConflict
		}
		body, err := json.Marshal(request.Input)
		if err != nil {
			return err
		}
		body, err = runtimeprotocol.CanonicalJSON(body)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		if hash != request.InputHash || hash != hex.EncodeToString(sum[:]) || !equalJSON(json.RawMessage(storedInput), request.Input) || !equalJSON(task.Ranking, request.Ranking) {
			return model.ErrConflict
		}
		requirements := []v1.Requirement{}
		for _, requirement := range snapshot.Definition.Requirements {
			if requirement.Scope == "plan" {
				requirements = append(requirements, requirement)
				continue
			}
			for _, id := range requirement.TaskIDs {
				if id == request.TaskID {
					requirements = append(requirements, requirement)
					break
				}
			}
		}
		if !equalJSON(requirements, request.Requirements) {
			return model.ErrConflict
		}
		if err = r.validatePlanSources(ctx, tx, info, snapshot.Definition); err != nil {
			return err
		}
		stored, locator, err := r.EvidenceInTransaction(ctx, tx, info, supplied.ReferenceID, supplied.ResultRevision)
		if err != nil {
			return err
		}
		if !equalJSON(stored.Record, supplied.Record) {
			return model.ErrConflict
		}
		mode := "live"
		var policy struct {
			QueryMode string `json:"query_mode"`
		}
		if json.Unmarshal(info.Scope.SourceScope, &policy) == nil && policy.QueryMode != "" {
			mode = policy.QueryMode
		}
		if stored.Record.Mode != mode {
			return model.ErrConflict
		}
		var query evidence.QueryContext
		if json.Unmarshal(stored.Record.Snapshot["query_context"], &query) != nil || !travelplan.ArgumentsMatch(request.Domain, request.Input, query.Arguments) {
			return model.ErrConflict
		}
		origin := "reused"
		var localCall any
		if locator == nil {
			var key, callOwner string
			if err = tx.QueryRow(ctx, "SELECT call_key,owner_task_id FROM travel.tool_calls WHERE id=$1 AND user_id=$2 AND conversation_id=$3", stored.Record.ToolCallRecordID, info.UserID, info.ConversationID).Scan(&key, &callOwner); err != nil {
				return err
			}
			if key == "exec:"+request.ExecutionStepID && callOwner == ref.OwnerID {
				origin = "queried"
				localCall = stored.Record.ToolCallRecordID
			}
		}
		if origin == "reused" && (request.TaskRevision > 1 || request.Generation > 1) {
			return model.ErrConflict
		}
		result, err = travelplan.EvidenceResult(request, stored)
		if err != nil {
			return err
		}
		if status == "succeeded" {
			if storedEvidence == nil || storedEvidenceRevision == nil || *storedEvidence != stored.ReferenceID || *storedEvidenceRevision != stored.ResultRevision {
				return model.ErrConflict
			}
			var saved v1.ExecutionResult
			if err = runtimeprotocol.DecodeStrict(storedResult, "ExecutionResult", &saved); err != nil {
				return err
			}
			if !equalJSON(saved, result) {
				return model.ErrConflict
			}
			result = saved
			return nil
		}
		if status != "pending" && status != "running" {
			return model.ErrConflict
		}
		output, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE travel.execution_steps SET status='succeeded',tool_call_record_id=$2,evidence_id=$3,result_revision=$4,evidence_origin=$5,result=$6::jsonb,completed_at=clock_timestamp(),updated_at=clock_timestamp(),error=NULL WHERE id=$1`, request.ExecutionStepID, localCall, stored.ReferenceID, stored.ResultRevision, origin, output)
		return err
	})
	return result, err
}
