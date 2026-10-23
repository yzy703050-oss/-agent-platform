package general

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/model"
	"context"
)

type RepositoryStore struct {
	Store       ports.CheckpointStore
	Input       contracts.RunInput
	Fingerprint string
}

func (s *RepositoryStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	if id != s.Input.Run.ID {
		return nil, false, ErrCheckpointIncompatible
	}
	cp, ok, e := s.Store.GetCheckpoint(ctx, s.Input.Scope, id)
	if e != nil || !ok {
		return nil, ok, e
	}
	if cp.Engine != "eino" || cp.EngineVersion != "v0.9.0-beta.1" || cp.FormatVersion != 1 || cp.Fingerprint != s.Fingerprint {
		return nil, false, ErrCheckpointIncompatible
	}
	return cp.Payload, true, nil
}
func (s *RepositoryStore) Set(ctx context.Context, id string, payload []byte) error {
	if id != s.Input.Run.ID {
		return ErrCheckpointIncompatible
	}
	return s.Store.SaveCheckpoint(ctx, model.CheckpointWrite{Scope: s.Input.Scope, Checkpoint: model.Checkpoint{RunID: id, Attempt: s.Input.Run.Attempt, Engine: "eino", EngineVersion: "v0.9.0-beta.1", FormatVersion: 1, Fingerprint: s.Fingerprint, Payload: payload}})
}
func (s *RepositoryStore) Delete(ctx context.Context, id string) error {
	if id != s.Input.Run.ID {
		return ErrCheckpointIncompatible
	}
	return s.Store.InvalidateCheckpoint(ctx, s.Input.Scope, id, s.Input.Run.Attempt)
}
