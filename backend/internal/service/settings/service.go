package settings

import (
	"agent-platform/backend/internal/model"
	"context"
)

type Store interface {
	GetSettings(context.Context, model.Scope) (model.UserSettings, error)
	UpdateSettings(context.Context, model.Scope, model.UserSettings) error
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func (s *Service) Get(ctx context.Context, scope model.Scope) (model.UserSettings, error) {
	return s.store.GetSettings(ctx, scope)
}
func (s *Service) Update(ctx context.Context, scope model.Scope, in model.UserSettings) error {
	if (in.Theme != "system" && in.Theme != "light" && in.Theme != "dark") || len(in.ReplyPreference) > 2000 {
		return model.ErrInvalidInput
	}
	return s.store.UpdateSettings(ctx, scope, in)
}
