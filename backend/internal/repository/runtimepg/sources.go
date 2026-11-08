package runtimepg

import (
	"context"
	"encoding/json"
	"time"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/toolquery"
	"github.com/jackc/pgx/v5"
)

func (s *Store) checkSources(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, refs []runtimev1.SourceRef) error {
	for _, ref := range refs {
		if ref.ID == "" || ref.Revision.Null {
			return model.ErrConflict
		}
		switch ref.Kind {
		case "message":
			var exists bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.schema()+".messages WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND context_epoch=$4 AND message_kind<>'model_output')", ref.ID, info.UserID, info.ConversationID, info.ContextEpoch).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return model.ErrConflict
			}
		case "memory":
			if !s.platform || !ref.Revision.Present {
				return model.ErrConflict
			}
			var raw []byte
			if err := tx.QueryRow(ctx, "SELECT snapshot FROM platform.user_memory_revisions WHERE user_id=$1 ORDER BY revision DESC LIMIT 1", info.UserID).Scan(&raw); err != nil {
				return err
			}
			var snapshot runtimev1.UserMemorySnapshot
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				return err
			}
			valid := false
			for _, memory := range snapshot.Memories {
				if memory.MemoryID == ref.ID && memory.Revision == ref.Revision.Value && memory.Status == "active" {
					valid = true
					break
				}
			}
			if !valid {
				return model.ErrConflict
			}
		case "thread_summary":
			if !ref.Revision.Present {
				return model.ErrConflict
			}
			table, column := "travel.threads", "last_main_context_epoch"
			if s.platform {
				table, column = "platform.agent_threads", "context_epoch"
			}
			var exists bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND "+column+"=$4 AND summary_version=$5)", ref.ID, info.UserID, info.ConversationID, info.ContextEpoch, ref.Revision.Value).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return model.ErrConflict
			}
		default:
			return model.ErrConflict // Cross-service evidence is checked by Reader/imports.
		}
	}
	return nil
}

func (s *Store) localEvidence(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, id string, revision int64) (runtimev1.SharedToolEvidenceRecord, error) {
	var record runtimev1.SharedToolEvidenceRecord
	var snapshot, sources []byte
	var fetched time.Time
	var expires *time.Time
	var epoch int64
	err := tx.QueryRow(ctx, `SELECT e.id,e.user_id,e.conversation_id,e.tool_call_record_id,e.domain,e.provider,e.schema_version,e.result_revision,e.mode,e.snapshot,e.source_refs,e.fetched_at,e.expires_at,c.context_epoch FROM `+s.schema()+`.tool_evidence e JOIN `+s.schema()+`.tool_calls c ON c.id=e.tool_call_record_id WHERE e.id=$1 AND e.result_revision=$2 AND e.user_id=$3 AND e.conversation_id=$4 AND c.status='succeeded'`, id, revision, info.UserID, info.ConversationID).Scan(&record.ID, &record.UserID, &record.ConversationID, &record.ToolCallRecordID, &record.Domain, &record.Provider, &record.SchemaVersion, &record.ResultRevision, &record.Mode, &snapshot, &sources, &fetched, &expires, &epoch)
	if err != nil {
		return record, err
	}
	if epoch != info.ContextEpoch {
		return record, model.ErrConflict
	}
	if err = json.Unmarshal(snapshot, &record.Snapshot); err != nil {
		return record, err
	}
	if err = json.Unmarshal(sources, &record.SourceRefs); err != nil {
		return record, err
	}
	if err = s.checkSources(ctx, tx, info, record.SourceRefs); err != nil {
		return record, err
	}
	record.FetchedAt = fetched.UTC().Format(time.RFC3339Nano)
	if expires != nil {
		text := expires.UTC().Format(time.RFC3339Nano)
		record.ExpiresAt = &text
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return record, err
	}
	if expires == nil || !expires.After(now) {
		return record, toolquery.ErrExpired
	}
	return record, nil
}
