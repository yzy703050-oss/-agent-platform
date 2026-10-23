package postgres

import (
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CreateConversation(ctx context.Context, scope model.Scope, agent, title string) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx, "INSERT INTO conversations(id,user_id,agent_type,title) VALUES($1,$2,$3,$4) RETURNING id,agent_type,title,created_at", uuid.NewString(), scope.UserID, agent, title).Scan(&c.ID, &c.AgentType, &c.Title, &c.CreatedAt)
	return c, err
}
func (r *Repository) FindConversation(ctx context.Context, scope model.Scope, id string) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx, "SELECT id,agent_type,title,created_at FROM conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL", id, scope.UserID).Scan(&c.ID, &c.AgentType, &c.Title, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, model.ErrNotFound
	}
	return c, err
}
func (r *Repository) ListConversations(ctx context.Context, scope model.Scope, cursor string, limit int) ([]model.Conversation, string, error) {
	if cursor != "" {
		if _, err := r.FindConversation(ctx, scope, cursor); err != nil {
			return nil, "", err
		}
	}
	rows, err := r.pool.Query(ctx, `SELECT id,agent_type,title,created_at FROM conversations WHERE user_id=$1 AND deleted_at IS NULL AND
 ($2='' OR (created_at,id)>(SELECT created_at,id FROM conversations WHERE id=$2 AND user_id=$1)) ORDER BY created_at,id LIMIT $3`, scope.UserID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result := []model.Conversation{}
	for rows.Next() {
		var c model.Conversation
		if err = rows.Scan(&c.ID, &c.AgentType, &c.Title, &c.CreatedAt); err != nil {
			return nil, "", err
		}
		result = append(result, c)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(result) > limit {
		result = result[:limit]
		next = result[limit-1].ID
	}
	return result, next, nil
}
func (r *Repository) RenameConversation(ctx context.Context, scope model.Scope, id, title string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE conversations SET title=$3 WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL", id, scope.UserID, title)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}
func (r *Repository) DeleteConversation(ctx context.Context, scope model.Scope, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockConversation(ctx, tx, scope, id); err != nil {
		return err
	}
	var active bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agent_runs WHERE conversation_id=$1 AND status IN('accepted','running','pausing','paused','resuming','cancelling'))", id).Scan(&active)
	if err != nil {
		return err
	}
	if active {
		return model.ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE conversations SET deleted_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) ListMessages(ctx context.Context, scope model.Scope, id string, after int64, limit int) ([]model.Message, error) {
	if _, err := r.FindConversation(ctx, scope, id); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, "SELECT id,conversation_id,run_id,role,content,tool_call_id,ordinal,tool_calls FROM messages WHERE conversation_id=$1 AND user_id=$2 AND ordinal>$3 ORDER BY ordinal LIMIT $4", id, scope.UserID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		var m model.Message
		var calls []byte
		if err = rows.Scan(&m.ID, &m.ConversationID, &m.RunID, &m.Role, &m.Content, &m.ToolCallID, &m.Ordinal, &calls); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(calls, &m.ToolCalls); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *Repository) GetSettings(ctx context.Context, scope model.Scope) (model.UserSettings, error) {
	var s model.UserSettings
	err := r.pool.QueryRow(ctx, "SELECT COALESCE(s.theme,'system'),COALESCE(s.reply_preference,'') FROM users u LEFT JOIN user_settings s ON u.id=s.user_id WHERE u.id=$1 AND ($2='' OR EXISTS(SELECT 1 FROM sessions a WHERE a.id=$2 AND a.user_id=u.id AND a.revoked_at IS NULL AND a.expires_at>now()))", scope.UserID, scope.SessionID).Scan(&s.Theme, &s.ReplyPreference)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, model.ErrNotFound
	}
	return s, err
}
func (r *Repository) UpdateSettings(ctx context.Context, scope model.Scope, in model.UserSettings) error {
	tag, err := r.pool.Exec(ctx, "INSERT INTO user_settings(user_id,theme,reply_preference) SELECT id,$2,$3 FROM users WHERE id=$1 ON CONFLICT(user_id) DO UPDATE SET theme=excluded.theme,reply_preference=excluded.reply_preference", scope.UserID, in.Theme, in.ReplyPreference)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}
