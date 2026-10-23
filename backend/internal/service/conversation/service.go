package conversation

import (
	"agent-platform/backend/internal/model"
	"context"
	"strings"
)

type Store interface {
	CreateConversation(context.Context, model.Scope, string, string) (model.Conversation, error)
	FindConversation(context.Context, model.Scope, string) (model.Conversation, error)
	ListConversations(context.Context, model.Scope, string, int) ([]model.Conversation, string, error)
	RenameConversation(context.Context, model.Scope, string, string) error
	DeleteConversation(context.Context, model.Scope, string) error
	ListMessages(context.Context, model.Scope, string, int64, int) ([]model.Message, error)
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func (s *Service) RequireAccess(ctx context.Context, scope model.Scope, id string) (model.Conversation, error) {
	return s.store.FindConversation(ctx, scope, id)
}
func (s *Service) Create(ctx context.Context, scope model.Scope, agent, title string) (model.Conversation, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "新对话"
	}
	if (agent != "general" && agent != "travel") || len(title) > 200 {
		return model.Conversation{}, model.ErrInvalidInput
	}
	return s.store.CreateConversation(ctx, scope, agent, title)
}
func (s *Service) Rename(ctx context.Context, scope model.Scope, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 {
		return model.ErrInvalidInput
	}
	return s.store.RenameConversation(ctx, scope, id, title)
}
func (s *Service) Delete(ctx context.Context, scope model.Scope, id string) error {
	return s.store.DeleteConversation(ctx, scope, id)
}
func (s *Service) List(ctx context.Context, scope model.Scope, cursor string, limit int) ([]model.Conversation, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", model.ErrInvalidInput
	}
	return s.store.ListConversations(ctx, scope, cursor, limit)
}
func (s *Service) Messages(ctx context.Context, scope model.Scope, id string, after int64, limit int) ([]model.Message, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, model.ErrInvalidInput
	}
	if _, err := s.RequireAccess(ctx, scope, id); err != nil {
		return nil, err
	}
	return s.store.ListMessages(ctx, scope, id, after, limit)
}
