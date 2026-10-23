package travelpreference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"

	"agent-platform/backend/internal/model"
)

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func (s *Service) Apply(ctx context.Context, ref model.TravelExecution, operationID string, cmd model.TravelPreferenceCommand) (model.TravelPreference, error) {
	if ref.Scope.UserID == "" || ref.Scope.SessionID == "" || ref.RunID == "" || ref.ConversationID == "" || ref.Attempt <= 0 || operationID == "" || cmd.Key == "" || strings.TrimSpace(cmd.Key) != cmd.Key || len(cmd.Key) > 80 || len(cmd.Value) > 2000 {
		return model.TravelPreference{}, model.ErrInvalidInput
	}
	for _, r := range cmd.Key {
		if unicode.IsControl(r) {
			return model.TravelPreference{}, model.ErrInvalidInput
		}
	}
	if cmd.Operation != "set" && cmd.Operation != "retract" {
		return model.TravelPreference{}, model.ErrInvalidInput
	}
	if cmd.Operation == "set" && strings.TrimSpace(cmd.Value) == "" {
		return model.TravelPreference{}, model.ErrInvalidInput
	}
	if cmd.Origin != "explicit" && cmd.Origin != "inferred" {
		return model.TravelPreference{}, model.ErrInvalidInput
	}
	if cmd.Operation == "retract" {
		cmd.Value = ""
	}
	data, err := json.Marshal(struct {
		Command model.TravelPreferenceCommand
		Origin  string
	}{cmd, cmd.Origin})
	if err != nil {
		return model.TravelPreference{}, err
	}
	hash := sha256.Sum256(data)
	return s.store.CommitTravelPreference(ctx, model.TravelPreferenceCommit{Execution: ref, OperationID: operationID, RequestHash: hex.EncodeToString(hash[:]), Command: cmd})
}
func (s *Service) ReadEffective(ctx context.Context, scope model.Scope, currentRunID string) ([]model.TravelPreference, error) {
	if scope.UserID == "" || scope.SessionID == "" || currentRunID == "" {
		return nil, model.ErrInvalidInput
	}
	return s.store.ReadTravelPreferences(ctx, scope, currentRunID)
}
