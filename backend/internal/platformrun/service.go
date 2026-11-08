// Package platformrun admits one durable Run per user question. Execution is
// claimed separately; admission never starts a goroutine or provider request.
package platformrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
)

type AdmissionStore interface {
	AdmitRun(context.Context, model.Scope, SubmitInput) (runtimev1.RunView, error)
}

type Service struct{ store AdmissionStore }

func New(store AdmissionStore) *Service { return &Service{store: store} }

func (s *Service) Admit(ctx context.Context, scope model.Scope, in SubmitInput) (runtimev1.RunView, error) {
	in, err := PrepareSubmit(scope, in)
	if err != nil {
		return runtimev1.RunView{}, err
	}
	return s.store.AdmitRun(ctx, scope, in)
}

// PrepareSubmit is shared with persistence so a future adapter cannot bypass
// immutable-input validation by calling AdmissionStore directly.
func PrepareSubmit(scope model.Scope, in SubmitInput) (SubmitInput, error) {
	if scope.UserID == "" || scope.SessionID == "" {
		return SubmitInput{}, model.ErrUnauthenticated
	}
	if in.ConversationID == "" || strings.TrimSpace(in.Content) == "" || !utf8.ValidString(in.Content) || len(in.Content) > 16000 || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 200 {
		return SubmitInput{}, model.ErrInvalidInput
	}
	h := sha256.Sum256([]byte(in.Content))
	hash := hex.EncodeToString(h[:])
	if in.RequestHash != "" && in.RequestHash != hash {
		return SubmitInput{}, model.ErrInvalidInput
	}
	in.RequestHash = hash
	return in, nil
}
