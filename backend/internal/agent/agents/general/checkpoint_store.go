package general

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
)

var ErrCheckpointIncompatible = errors.New("checkpoint incompatible")
var ErrCheckpointMissing = errors.New("checkpoint missing")

// The payload is owned by Eino; the envelope only guards execution identity and configuration.
type checkpointEnvelope struct {
	Version     int    `json:"version"`
	RunID       string `json:"run_id"`
	Fingerprint string `json:"fingerprint"`
	Payload     []byte `json:"payload"`
}
type checkpointStore struct {
	requirePresent     bool
	inner              adk.CheckPointStore
	runID, fingerprint string
	unsafe             atomic.Bool
}

func (s *checkpointStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	if id != s.runID {
		return nil, false, ErrCheckpointIncompatible
	}
	b, ok, e := s.inner.Get(ctx, id)
	if e == nil && !ok && s.requirePresent {
		return nil, false, ErrCheckpointMissing
	}
	if e != nil || !ok {
		return nil, ok, e
	}
	var v checkpointEnvelope
	if json.Unmarshal(b, &v) != nil || v.Version != 1 || v.RunID != s.runID || v.Fingerprint != s.fingerprint || len(v.Payload) == 0 {
		return nil, false, ErrCheckpointIncompatible
	}
	return v.Payload, true, nil
}
func (s *checkpointStore) Set(ctx context.Context, id string, payload []byte) error {
	if s.unsafe.Load() {
		return errors.New("checkpoint unsafe after forced streaming stop")
	}
	if id != s.runID {
		return ErrCheckpointIncompatible
	}
	b, e := json.Marshal(checkpointEnvelope{Version: 1, RunID: s.runID, Fingerprint: s.fingerprint, Payload: payload})
	if e != nil {
		return e
	}
	return s.inner.Set(ctx, id, b)
}
func (s *checkpointStore) Delete(ctx context.Context, id string) error {
	if id != s.runID {
		return ErrCheckpointIncompatible
	}
	if d, ok := s.inner.(adk.CheckPointDeleter); ok {
		return d.Delete(ctx, id)
	}
	return errors.New("checkpoint deletion unsupported")
}
