package handler

import (
	"agent-platform/backend/internal/agent/manager"
	chatapp "agent-platform/backend/internal/application/chat"
	"agent-platform/backend/internal/middleware"
	"agent-platform/backend/internal/model"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

type API struct {
	Manager       *manager.Manager
	Pages         *manager.LeaseManager
	Chat          *chatapp.Application
	Users         *usersvc.Service
	Auth          *authsvc.Service
	Conversations *convsvc.Service
	Settings      *settingssvc.Service
	SecureCookies bool
}

func failure(c *gin.Context, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, model.ErrInvalidInput):
		status, code = 400, "invalid_input"
	case errors.Is(err, model.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, model.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, model.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, model.ErrIdempotencyConflict):
		status, code = 409, "idempotency_conflict"
	case errors.Is(err, model.ErrQuota):
		status, code = 429, "run_quota_exceeded"
	case errors.Is(err, model.ErrModelUnavailable):
		status, code = 503, "model_unavailable"
	case errors.Is(err, model.ErrAgentUnavailable):
		status, code = 503, "agent_unavailable"
	case errors.Is(err, model.ErrCheckpointIncompatible):
		status, code = 409, "checkpoint_incompatible"
	}
	middleware.Reject(c, status, code)
}
func bind(c *gin.Context, v any) bool {
	if c.ContentType() != "application/json" || c.ShouldBindJSON(v) != nil {
		middleware.Reject(c, 400, "invalid_input")
		return false
	}
	return true
}
func (a *API) Register(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &in) {
		return
	}
	user, err := a.Auth.Register(c.Request.Context(), in.Username, in.Password)
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(201, user)
}
func (a *API) Login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &in) {
		return
	}
	session, err := a.Auth.Login(c.Request.Context(), in.Username, in.Password)
	if err != nil {
		failure(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "session", Value: session.Token, Path: "/", HttpOnly: true, Secure: a.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 3600})
	c.JSON(200, gin.H{"user": session.User, "csrf_token": session.CSRFToken})
}
func (a *API) Logout(c *gin.Context) {
	token, _ := c.Cookie("session")
	if err := a.Auth.Logout(c.Request.Context(), token); err != nil {
		failure(c, err)
		return
	}
	if a.Pages != nil {
		if err := a.Pages.EndSession(c.Request.Context(), middleware.Scope(c)); err != nil {
			failure(c, err)
			return
		}
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "session", Value: "", Path: "/", HttpOnly: true, Secure: a.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	c.Status(204)
}
func (a *API) Me(c *gin.Context) {
	user, err := a.Users.Get(c.Request.Context(), middleware.Scope(c))
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, user)
}
func (a *API) GetSettings(c *gin.Context) {
	s, err := a.Settings.Get(c.Request.Context(), middleware.Scope(c))
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, s)
}
func (a *API) UpdateSettings(c *gin.Context) {
	var in model.UserSettings
	if !bind(c, &in) {
		return
	}
	if err := a.Settings.Update(c.Request.Context(), middleware.Scope(c), in); err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, in)
}
func pageLimit(c *gin.Context) (int, error) {
	if raw := c.Query("limit"); raw != "" {
		return strconv.Atoi(raw)
	}
	return 50, nil
}
func (a *API) ListConversations(c *gin.Context) {
	limit, err := pageLimit(c)
	if err != nil {
		failure(c, model.ErrInvalidInput)
		return
	}
	out, next, err := a.Conversations.List(c.Request.Context(), middleware.Scope(c), c.Query("cursor"), limit)
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, gin.H{"items": out, "next_cursor": next})
}
func (a *API) CreateConversation(c *gin.Context) {
	var in struct {
		AgentType string `json:"agent_type"`
		Title     string `json:"title"`
	}
	if !bind(c, &in) {
		return
	}
	out, err := a.Conversations.Create(c.Request.Context(), middleware.Scope(c), in.AgentType, in.Title)
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(201, out)
}
func (a *API) RenameConversation(c *gin.Context) {
	var in struct {
		Title string `json:"title"`
	}
	if !bind(c, &in) {
		return
	}
	if err := a.Conversations.Rename(c.Request.Context(), middleware.Scope(c), c.Param("id"), in.Title); err != nil {
		failure(c, err)
		return
	}
	c.Status(204)
}
func (a *API) DeleteConversation(c *gin.Context) {
	if err := a.Conversations.Delete(c.Request.Context(), middleware.Scope(c), c.Param("id")); err != nil {
		failure(c, err)
		return
	}
	c.Status(204)
}
func (a *API) Messages(c *gin.Context) {
	limit, err := pageLimit(c)
	if err != nil {
		failure(c, model.ErrInvalidInput)
		return
	}
	var after int64
	if raw := c.Query("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			failure(c, model.ErrInvalidInput)
			return
		}
	}
	out, err := a.Conversations.Messages(c.Request.Context(), middleware.Scope(c), c.Param("id"), after, limit)
	if err != nil {
		failure(c, err)
		return
	}
	var next int64
	if len(out) > 0 {
		next = out[len(out)-1].Ordinal
	}
	c.JSON(200, gin.H{"items": out, "next_after": next})
}
func (a *API) SubmitRun(c *gin.Context) {
	conv, err := a.Conversations.RequireAccess(c.Request.Context(), middleware.Scope(c), c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	if conv.AgentType == "travel" {
		middleware.Reject(c, 503, "agent_unavailable")
		return
	}
	if a.Chat == nil || !a.Chat.Available() {
		middleware.Reject(c, 503, "model_unavailable")
		return
	}
	var in chatapp.SubmitInput
	if !bind(c, &in) {
		return
	}
	in.ConversationID = conv.ID
	if a.Pages == nil {
		failure(c, model.ErrConflict)
		return
	}
	if err = a.Pages.Verify(c.Request.Context(), middleware.Scope(c), in.PageID, c.GetHeader("X-Page-Token")); err != nil {
		failure(c, err)
		return
	}
	in.IdempotencyKey = c.GetHeader("Idempotency-Key")
	run, err := a.Chat.Submit(c.Request.Context(), middleware.Scope(c), in)
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(202, run)
}
func (a *API) Agents(c *gin.Context) {
	available := a.Chat != nil && a.Chat.Available()
	reason := ""
	if !available {
		reason = "model_not_connected"
	}
	c.JSON(200, gin.H{"items": []gin.H{{"id": "general", "available": available, "reason": reason}, {"id": "travel", "available": false, "reason": "not_connected"}}})
}
