package user

import (
	"agent-platform/backend/internal/model"
	"context"
)

type Store interface {
	FindUser(context.Context, model.Scope) (model.User, error)
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func (s *Service) Get(ctx context.Context, scope model.Scope) (model.User, error) {
	return s.store.FindUser(ctx, scope)
}
