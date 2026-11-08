package travelpg

import (
	"context"
	"errors"
	"strings"

	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) policySource(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, source v1.SourceRef) (v1.EvidenceIdentity, error) {
	var identity v1.EvidenceIdentity
	err := tx.QueryRow(ctx, `SELECT e.id,e.result_revision FROM travel.tool_evidence e JOIN travel.tool_calls c ON c.id=e.tool_call_record_id WHERE e.domain='policy' AND e.user_id=$1 AND e.conversation_id=$2 AND c.thread_id=$3 AND c.context_epoch=$4 AND c.status='succeeded' AND e.expires_at>clock_timestamp() AND (NOT $5::boolean OR e.result_revision=$6) AND e.snapshot#>'{raw,fragments}' @> jsonb_build_array(jsonb_build_object('id',$7::text)) ORDER BY e.fetched_at DESC LIMIT 1`, info.UserID, info.ConversationID, info.ThreadID, info.ContextEpoch, source.Revision.Present, source.Revision.Value, source.ID).Scan(&identity.EvidenceID, &identity.ResultRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		rows, e := tx.Query(ctx, `SELECT evidence_id,origin_revision FROM travel.evidence_imports WHERE user_id=$1 AND conversation_id=$2 AND payload->>'domain'='policy' AND (NOT $3::boolean OR origin_revision=$4) AND payload#>'{snapshot,raw,fragments}' @> jsonb_build_array(jsonb_build_object('id',$5::text)) ORDER BY imported_at DESC`, info.UserID, info.ConversationID, source.Revision.Present, source.Revision.Value, source.ID)
		if e != nil {
			return identity, e
		}
		candidates := []v1.EvidenceIdentity{}
		for rows.Next() {
			var candidate v1.EvidenceIdentity
			if e = rows.Scan(&candidate.EvidenceID, &candidate.ResultRevision); e != nil {
				rows.Close()
				return identity, e
			}
			candidates = append(candidates, candidate)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return identity, e
		}
		for _, candidate := range candidates {
			if _, _, e = r.EvidenceInTransaction(ctx, tx, info, candidate.EvidenceID, candidate.ResultRevision); e == nil {
				return candidate, nil
			}
		}
		return identity, model.ErrConflict
	}
	if err != nil {
		return identity, err
	}
	if _, _, err = r.EvidenceInTransaction(ctx, tx, info, identity.EvidenceID, identity.ResultRevision); err != nil {
		return identity, err
	}
	return identity, nil
}

// ImportedPlanSources discovers exact authorized dependencies before service
// network preflight; the commit rechecks their immutable body and scope.
func (r *Repository) ImportedPlanSources(ctx context.Context, ref v1.ExecRef, definition v1.PlanDefinition) ([]v1.EvidenceIdentity, error) {
	identities := []v1.EvidenceIdentity{}
	err := r.OwnerTransaction(ctx, ref, "commit", "plan", func(tx pgx.Tx, info runtimeports.Execution) error {
		for _, requirement := range definition.Requirements {
			for _, source := range requirement.Sources {
				var identity v1.EvidenceIdentity
				if source.Kind == "policy" {
					var err error
					identity, err = r.policySource(ctx, tx, info, source)
					if err != nil {
						return err
					}
				} else if source.Kind == "evidence" {
					if !source.Revision.Present || source.Revision.Null {
						return model.ErrConflict
					}
					identity = v1.EvidenceIdentity{EvidenceID: source.ID, ResultRevision: source.Revision.Value}
				} else {
					continue
				}
				if strings.HasPrefix(identity.EvidenceID, "remote:") {
					identities = append(identities, identity)
				}
			}
		}
		return nil
	})
	return identities, err
}
