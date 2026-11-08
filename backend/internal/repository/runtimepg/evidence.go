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
	"github.com/jackc/pgx/v5"
)

func (s *Store) evidenceContext(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef) (runtimeports.Execution, error) {
	var raw []byte
	var data scopeContext
	if err := tx.QueryRow(ctx, "SELECT control.evidence_call_context($1,$2)", ref.ScopeID, ref.ScopeHash).Scan(&raw); err != nil {
		return runtimeports.Execution{}, err
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return runtimeports.Execution{}, err
	}
	if len(data.Scope.Targets) == 0 {
		return runtimeports.Execution{}, model.ErrConflict
	}
	return s.authorize(ctx, tx, ref, "evidence", data.Scope.Targets[0])
}

func (s *Store) LocalOrImport(ctx context.Context, ref runtimev1.ExecRef, id string, revision int64) (evidence.View, *evidence.Locator, error) {
	tx, err := s.tx(ctx)
	if err != nil {
		return evidence.View{}, nil, err
	}
	defer tx.Rollback(ctx)
	info, err := s.evidenceContext(ctx, tx, ref)
	if err != nil {
		return evidence.View{}, nil, err
	}
	view, locator, err := s.EvidenceInTransaction(ctx, tx, info, id, revision)
	if err != nil {
		return evidence.View{}, nil, err
	}
	return view, locator, tx.Commit(ctx)
}

// EvidenceInTransaction is for an already-authorized owner transaction. It
// performs only local fact/import reads; remote liveness checks precede it.
func (s *Store) EvidenceInTransaction(ctx context.Context, tx pgx.Tx, info runtimeports.Execution, id string, revision int64) (evidence.View, *evidence.Locator, error) {
	if !contains(info.Scope.Capabilities, "evidence") {
		return evidence.View{}, nil, model.ErrConflict
	}
	var locator evidence.Locator
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT origin_service,origin_id,origin_revision,content_hash,payload FROM "+s.schema()+".evidence_imports WHERE evidence_id=$1 AND origin_revision=$2 AND user_id=$3 AND conversation_id=$4", id, revision, info.UserID, info.ConversationID).Scan(&locator.OriginService, &locator.OriginID, &locator.OriginRevision, &locator.ContentHash, &raw)
	if err == nil {
		if !evidence.ManifestAllows(info.Scope.SourceScope, locator) {
			return evidence.View{}, nil, model.ErrConflict
		}
		record, err := evidence.Decode(raw, locator)
		if err != nil {
			return evidence.View{}, nil, err
		}
		if err = freshRecord(record, info); err != nil {
			return evidence.View{}, nil, err
		}
		return evidence.View{ReferenceID: id, ResultRevision: revision, Record: record}, &locator, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return evidence.View{}, nil, err
	}
	record, err := s.localEvidence(ctx, tx, info, id, revision)
	if err != nil {
		return evidence.View{}, nil, err
	}
	if !contains(info.Scope.Targets, record.Domain) {
		return evidence.View{}, nil, model.ErrConflict
	}
	return evidence.View{ReferenceID: id, ResultRevision: revision, Record: record}, nil, nil
}

func freshRecord(record runtimev1.SharedToolEvidenceRecord, info runtimeports.Execution) error {
	if record.UserID != info.UserID || record.ConversationID != info.ConversationID || !contains(info.Scope.Targets, record.Domain) || record.ExpiresAt == nil {
		return model.ErrConflict
	}
	expiry, err := time.Parse(time.RFC3339Nano, *record.ExpiresAt)
	if err != nil || !expiry.After(time.Now()) {
		return model.ErrConflict
	}
	return nil
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// A nil payload is a read-only preflight. Every actual insert repeats these
// checks under the current local lease and authorization guards after network I/O.
func (s *Store) SaveImport(ctx context.Context, ref runtimev1.ExecRef, l evidence.Locator, id string, raw json.RawMessage) (evidence.View, error) {
	if !evidence.ValidLocator(l) || l.OriginService == s.schema() || id == "" {
		return evidence.View{}, model.ErrInvalidInput
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return evidence.View{}, err
	}
	defer tx.Rollback(ctx)
	info, err := s.evidenceContext(ctx, tx, ref)
	if err != nil {
		return evidence.View{}, err
	}
	if !evidence.ManifestAllows(info.Scope.SourceScope, l) {
		return evidence.View{}, model.ErrConflict
	}
	if raw == nil {
		return evidence.View{}, tx.Commit(ctx)
	}
	record, err := evidence.Decode(raw, l)
	if err != nil {
		return evidence.View{}, err
	}
	if err = freshRecord(record, info); err != nil {
		return evidence.View{}, err
	}
	var collision bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.schema()+".tool_evidence WHERE id=$1)", id).Scan(&collision); err != nil || collision {
		if err == nil {
			err = model.ErrConflict
		}
		return evidence.View{}, err
	}
	authorization, _ := json.Marshal(map[string]any{"scope_id": ref.ScopeID, "scope_hash": ref.ScopeHash, "context_epoch": info.ContextEpoch, "locator": l})
	if _, err = tx.Exec(ctx, `INSERT INTO `+s.schema()+`.evidence_imports(origin_service,origin_id,origin_revision,evidence_id,user_id,conversation_id,content_hash,authorization_source,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb) ON CONFLICT DO NOTHING`, l.OriginService, l.OriginID, l.OriginRevision, id, info.UserID, info.ConversationID, l.ContentHash, authorization, raw); err != nil {
		return evidence.View{}, err
	}
	var existingHash, existingID, userID, conversationID string
	if err = tx.QueryRow(ctx, "SELECT content_hash,evidence_id,user_id,conversation_id FROM "+s.schema()+".evidence_imports WHERE origin_service=$1 AND origin_id=$2 AND origin_revision=$3", l.OriginService, l.OriginID, l.OriginRevision).Scan(&existingHash, &existingID, &userID, &conversationID); err != nil {
		return evidence.View{}, err
	}
	if existingHash != l.ContentHash || existingID != id || userID != info.UserID || conversationID != info.ConversationID {
		return evidence.View{}, model.ErrConflict
	}
	if _, err = s.evidenceContext(ctx, tx, ref); err != nil {
		return evidence.View{}, err
	}
	return evidence.View{ReferenceID: id, ResultRevision: l.OriginRevision, Record: record}, tx.Commit(ctx)
}

func (s *Store) ExportEvidence(ctx context.Context, peer evidence.PeerScope, l evidence.Locator) (json.RawMessage, error) {
	l.OriginService = s.schema()
	if !evidence.ValidLocator(l) {
		return nil, model.ErrInvalidInput
	}
	tx, err := s.tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	var scope control.AuthorizedScope
	check := func() error {
		if err := tx.QueryRow(ctx, "SELECT control.peer_evidence_context($1,$2,$3,$4,$5,$6)", peer.ScopeID, peer.ScopeHash, peer.PeerService, l.OriginID, l.OriginRevision, l.ContentHash).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &scope)
	}
	if err = check(); err != nil {
		return nil, err
	}
	info := runtimeports.Execution{Scope: scope, UserID: scope.SubjectID, ConversationID: scope.ConversationID, ContextEpoch: scope.ContextEpoch}
	record, err := s.localEvidence(ctx, tx, info, l.OriginID, l.OriginRevision)
	if err != nil {
		return nil, err
	}
	if !contains(scope.Targets, record.Domain) {
		return nil, model.ErrConflict
	}
	body, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	body, err = runtimeprotocol.CanonicalJSON(body)
	if err != nil {
		return nil, err
	}
	hash, err := evidence.ContentHash(body)
	if err != nil || hash != l.ContentHash {
		return nil, model.ErrConflict
	}
	if err = check(); err != nil {
		return nil, err
	}
	return body, tx.Commit(ctx)
}
