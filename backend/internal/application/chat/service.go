package chat

import (
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type Conversations interface {
	RequireAccess(context.Context, model.Scope, string) (model.Conversation, error)
}
type Application struct {
	conversations Conversations
	manager       *manager.Manager
}
type SubmitInput struct {
	ConversationID string `json:"-"`
	Content        string `json:"content"`
	IdempotencyKey string `json:"-"`
	PageID         string `json:"page_id"`
}

func New(c Conversations, m *manager.Manager) *Application { return &Application{c, m} }
func (a *Application) Available() bool                     { return a.manager.Available("general") }
func (a *Application) Submit(ctx context.Context, s model.Scope, in SubmitInput) (model.Run, error) {
	if strings.TrimSpace(in.Content) == "" || len(in.Content) > 16000 || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 200 || in.PageID == "" {
		return model.Run{}, model.ErrInvalidInput
	}
	conv, e := a.conversations.RequireAccess(ctx, s, in.ConversationID)
	if e != nil {
		return model.Run{}, e
	}
	if conv.AgentType == "travel" {
		return model.Run{}, model.ErrAgentUnavailable
	}
	sum := sha256.Sum256([]byte(in.Content))
	s.PageID = in.PageID
	return a.manager.Submit(ctx, model.AdmitRunInput{Scope: s, ConversationID: conv.ID, Content: in.Content, IdempotencyKey: in.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:])}, conv.AgentType)
}
