package travelpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	"agent-platform/backend/internal/service/travelplan"
	"agent-platform/backend/internal/toolquery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const planKernelVersion = "travel-plan-v1"

func validPlanOperation(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 160 }

func (r *Repository) CreatePlan(ctx context.Context, ref v1.ExecRef, operation string, definition v1.PlanDefinition) (v1.CommitResult, error) {
	var result v1.CommitResult
	if !validPlanOperation(operation) {
		return result, model.ErrInvalidInput
	}
	if err := travelplan.ValidateDefinition(definition); err != nil {
		return result, err
	}
	args, hash, err := planArguments("create_plan", definition)
	if err != nil {
		return result, err
	}
	err = r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		prior, exists, err := findPlanAction(ctx, tx, info, ref.OwnerID, operation, hash)
		if err != nil {
			return err
		}
		if exists {
			result = prior
			return nil
		}
		var existing bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM travel.plans WHERE travel_thread_id=$1)", info.ThreadID).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return model.ErrConflict
		}
		snapshot, err := travelplan.InitialSnapshot(uuid.NewString(), info.ThreadID, definition)
		if err != nil {
			return err
		}
		if err = r.validatePlanSources(ctx, tx, info, snapshot.Definition); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO travel.plans(id,user_id,conversation_id,source_run_id,source_task_id,travel_thread_id,current_revision,schema_version,kernel_version,title,lifecycle) VALUES($1,$2,$3,$4,$5,$6,1,1,$7,$8,'active')`, snapshot.PlanID, info.UserID, info.ConversationID, info.RunID, ref.OwnerID, info.ThreadID, planKernelVersion, snapshot.Definition.Title); err != nil {
			return err
		}
		if err = savePlanRevision(ctx, tx, ref, info, snapshot, nil, operation, "initial definition"); err != nil {
			return err
		}
		if err = arrangePlanSteps(ctx, tx, ref, info, snapshot, nil, nil); err != nil {
			return err
		}
		result = v1.CommitResult{OperationID: operation, PlanID: snapshot.PlanID, PreviousRevision: 0, CommittedRevision: 1, Changed: true, InvalidatedTaskIDs: []string{}, RefreshedItemIDs: []string{}}
		return savePlanAction(ctx, tx, ref, info, "create_plan", args, hash, "initial definition", result)
	})
	return result, err
}

func (r *Repository) PatchPlan(ctx context.Context, ref v1.ExecRef, operation string, patch v1.PlanPatch) (v1.CommitResult, error) {
	var result v1.CommitResult
	if !validPlanOperation(operation) {
		return result, model.ErrInvalidInput
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return result, err
	}
	var checked v1.PlanPatch
	if err = runtimeprotocol.DecodeStrict(raw, "PlanPatch", &checked); err != nil {
		return result, err
	}
	patch = checked
	args, hash, err := planArguments("apply_patch", patch)
	if err != nil {
		return result, err
	}
	err = r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		prior, exists, err := findPlanAction(ctx, tx, info, ref.OwnerID, operation, hash)
		if err != nil {
			return err
		}
		if exists {
			result = prior
			return nil
		}
		base, err := loadPlanSnapshot(ctx, tx, info, patch.PlanID, true)
		if err != nil {
			return err
		}
		change, err := travelplan.ProposePatch(base, patch)
		if err != nil {
			return err
		}
		if err = r.validatePlanSources(ctx, tx, info, change.Snapshot.Definition); err != nil {
			return err
		}
		if err = r.validateSelections(ctx, tx, info, change.Snapshot, patch); err != nil {
			return err
		}
		if change.Changed {
			body, _ := json.Marshal(patch)
			if err = savePlanRevision(ctx, tx, ref, info, change.Snapshot, body, operation, patch.Reason); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE travel.plans SET current_revision=$2,title=$3,updated_at=clock_timestamp() WHERE id=$1 AND current_revision=$4", patch.PlanID, change.Snapshot.Revision, change.Snapshot.Definition.Title, base.Revision); err != nil {
				return err
			}
		}
		if err = arrangePlanSteps(ctx, tx, ref, info, change.Snapshot, change.InvalidatedTaskIDs, change.RefreshedItemIDs); err != nil {
			return err
		}
		invalidated := change.InvalidatedTaskIDs
		if invalidated == nil {
			invalidated = []string{}
		}
		refreshed := change.RefreshedItemIDs
		if refreshed == nil {
			refreshed = []string{}
		}
		result = v1.CommitResult{OperationID: operation, PlanID: patch.PlanID, PreviousRevision: base.Revision, CommittedRevision: change.Snapshot.Revision, Changed: change.Changed, InvalidatedTaskIDs: invalidated, RefreshedItemIDs: refreshed}
		return savePlanAction(ctx, tx, ref, info, "apply_patch", args, hash, patch.Reason, result)
	})
	return result, err
}

func loadPlanSnapshot(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, id string, lock bool) (v1.PlanSnapshot, error) {
	var snapshot v1.PlanSnapshot
	var raw []byte
	clause := ""
	if lock {
		clause = " FOR UPDATE OF p"
	}
	err := tx.QueryRow(ctx, `SELECT r.snapshot FROM travel.plans p JOIN travel.revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.id=$1 AND p.user_id=$2 AND p.conversation_id=$3 AND p.travel_thread_id=$4`+clause, id, info.UserID, info.ConversationID, info.ThreadID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, model.ErrNotFound
	}
	if err != nil {
		return snapshot, err
	}
	if err = runtimeprotocol.DecodeStrict(raw, "PlanSnapshot", &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func savePlanRevision(ctx context.Context, tx pgx.Tx, ref v1.ExecRef, info runtimeports.Execution, snapshot v1.PlanSnapshot, patch json.RawMessage, operation, reason string) error {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var proposed any
	if patch != nil {
		proposed = patch
	}
	_, err = tx.Exec(ctx, `INSERT INTO travel.revisions(plan_id,revision,user_id,conversation_id,source_run_id,source_task_id,delegation_id,schema_version,base_revision,snapshot,patch,reason,operation_id) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9::jsonb,$10::jsonb,$11,$12)`, snapshot.PlanID, snapshot.Revision, info.UserID, info.ConversationID, info.RunID, ref.OwnerID, info.Scope.DelegationID, snapshot.Revision-1, body, proposed, reason, operation)
	return err
}

func (r *Repository) validatePlanSources(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, definition v1.PlanDefinition) error {
	for _, requirement := range definition.Requirements {
		for _, source := range requirement.Sources {
			if source.Revision.Null {
				return model.ErrConflict
			}
			if source.Kind == "policy" {
				if _, err := r.policySource(ctx, tx, info, source); err != nil {
					return err
				}
				continue
			}
			if source.Kind == "memory" {
				if !source.Revision.Present || source.Revision.Null {
					return model.ErrConflict
				}
				allowed := false
				for _, target := range info.Scope.Targets {
					if !strings.HasPrefix(target, "memory:") {
						continue
					}
					var raw []byte
					if err := tx.QueryRow(ctx, "SELECT control.read_related_memory($1,$2,$3)", info.Scope.ScopeID, info.Scope.ScopeHash, strings.TrimPrefix(target, "memory:")).Scan(&raw); err != nil {
						return err
					}
					var entries []v1.MemoryRecord
					if err := json.Unmarshal(raw, &entries); err != nil {
						return err
					}
					for _, entry := range entries {
						if entry.MemoryID == source.ID && entry.Revision == source.Revision.Value && entry.Status == "active" {
							allowed = true
						}
					}
				}
				if !allowed {
					return model.ErrConflict
				}
				continue
			}
			if source.Kind == "evidence" {
				if !source.Revision.Present {
					return model.ErrConflict
				}
				if _, _, err := r.EvidenceInTransaction(ctx, tx, info, source.ID, source.Revision.Value); err != nil {
					return err
				}
				continue
			}
			if source.Kind == "thread_summary" {
				if !source.Revision.Present {
					return model.ErrConflict
				}
				var allowed bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM travel.threads WHERE id=$1 AND id=$2 AND user_id=$3 AND conversation_id=$4 AND last_main_context_epoch=$5 AND summary_version=$6)", source.ID, info.ThreadID, info.UserID, info.ConversationID, info.ContextEpoch, source.Revision.Value).Scan(&allowed); err != nil {
					return err
				}
				if !allowed {
					return model.ErrConflict
				}
				continue
			}
			if source.Kind != "message" {
				return model.ErrInvalidInput
			}
			if source.Revision.Present && source.Revision.Value != 1 {
				return model.ErrConflict
			}
			var allowed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM travel.messages WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND thread_id=$4 AND context_epoch=$5 AND message_kind<>'model_output' AND ($6<>'user' OR role='user'))`, source.ID, info.UserID, info.ConversationID, info.ThreadID, info.ContextEpoch, requirement.Origin).Scan(&allowed); err != nil {
				return err
			}
			if !allowed {
				return model.ErrConflict
			}
		}
	}
	return nil
}

func arrangePlanSteps(ctx context.Context, tx pgx.Tx, ref v1.ExecRef, info runtimeports.Execution, snapshot v1.PlanSnapshot, invalidated, refreshed []string) error {
	for _, id := range invalidated {
		if _, err := tx.Exec(ctx, "UPDATE travel.execution_steps SET status='stale',updated_at=clock_timestamp() WHERE plan_id=$1 AND task_id=$2 AND status<>'stale'", snapshot.PlanID, id); err != nil {
			return err
		}
	}
	versions := map[string]int64{}
	for _, v := range snapshot.TaskVersions {
		versions[v.TaskID] = v.TaskRevision
	}
	for _, task := range snapshot.Definition.Tasks {
		for _, item := range task.Items {
			var generation int64
			if err := tx.QueryRow(ctx, "SELECT COALESCE(max(generation),0) FROM travel.execution_steps WHERE plan_id=$1 AND task_id=$2 AND task_revision=$3 AND item_id=$4", snapshot.PlanID, task.TaskID, versions[task.TaskID], item.ItemID).Scan(&generation); err != nil {
				return err
			}
			refresh := false
			for _, id := range refreshed {
				if item.ItemID == id {
					refresh = true
				}
			}
			if generation > 0 && !refresh {
				continue
			}
			if refresh {
				if _, err := tx.Exec(ctx, "UPDATE travel.execution_steps SET status='stale',updated_at=clock_timestamp() WHERE plan_id=$1 AND task_id=$2 AND task_revision=$3 AND item_id=$4 AND status<>'stale'", snapshot.PlanID, task.TaskID, versions[task.TaskID], item.ItemID); err != nil {
					return err
				}
			}
			input, reason, err := travelplan.QueryInput(snapshot.Definition, task, item)
			if err != nil {
				return err
			}
			body, err := json.Marshal(input)
			if err != nil {
				return err
			}
			canonical, err := runtimeprotocol.CanonicalJSON(body)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(canonical)
			status := "pending"
			var blocked any
			if reason != "" {
				status = "blocked"
				blocked = reason
			}
			if _, err = tx.Exec(ctx, `INSERT INTO travel.execution_steps(id,user_id,conversation_id,run_id,delegation_id,owner_task_id,plan_id,task_id,item_id,domain,plan_revision,task_revision,generation,input,input_hash,blocked_reason,status,lease_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18)`, uuid.NewString(), info.UserID, info.ConversationID, info.RunID, info.Scope.DelegationID, ref.OwnerID, snapshot.PlanID, task.TaskID, item.ItemID, item.Domain, snapshot.Revision, versions[task.TaskID], generation+1, canonical, hex.EncodeToString(hash[:]), blocked, status, ref.Lease.Epoch); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) ViewPlan(ctx context.Context, ref v1.ExecRef, id string) (v1.PlanView, error) {
	var view v1.PlanView
	err := r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		var err error
		view.Snapshot, err = loadPlanSnapshot(ctx, tx, info, id, false)
		if err != nil {
			return err
		}
		view.Items = []v1.ItemView{}
		versions := map[string]int64{}
		for _, v := range view.Snapshot.TaskVersions {
			versions[v.TaskID] = v.TaskRevision
		}
		for _, task := range view.Snapshot.Definition.Tasks {
			for _, item := range task.Items {
				row := v1.ItemView{TaskID: task.TaskID, TaskRevision: versions[task.TaskID], ItemID: item.ItemID, NeedsQuery: true}
				var evidenceID *string
				var evidenceRevision *int64
				if err = tx.QueryRow(ctx, "SELECT status,blocked_reason,evidence_id,result_revision FROM travel.execution_steps WHERE plan_id=$1 AND task_id=$2 AND task_revision=$3 AND item_id=$4 ORDER BY generation DESC LIMIT 1", id, task.TaskID, row.TaskRevision, item.ItemID).Scan(&row.Status, &row.Reason, &evidenceID, &evidenceRevision); err != nil {
					return err
				}
				if row.Status == "succeeded" {
					version := row.TaskRevision
					row.LastSuccessfulTaskRevision = &version
					if evidenceID == nil || evidenceRevision == nil {
						return model.ErrConflict
					}
					if _, _, err = r.EvidenceInTransaction(ctx, tx, info, *evidenceID, *evidenceRevision); err != nil {
						if !errors.Is(err, toolquery.ErrExpired) && !errors.Is(err, model.ErrConflict) && !errors.Is(err, pgx.ErrNoRows) {
							return err
						}
						row.Status = "stale"
						reason := "evidence expired or source unavailable"
						row.Reason = &reason
					} else {
						row.NeedsQuery = false
					}
				}
				view.Items = append(view.Items, row)
			}
		}
		return nil
	})
	return view, err
}
