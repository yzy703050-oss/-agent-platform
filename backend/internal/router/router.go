package router

import (
	"agent-platform/backend/internal/handler"
	"agent-platform/backend/internal/middleware"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

type Dependencies struct {
	Users         *usersvc.Service
	Auth          *authsvc.Service
	Conversations *convsvc.Service
	Settings      *settingssvc.Service
	SecureCookies bool
	AllowedOrigin string
	Ready         func(context.Context) error
}

func New(d Dependencies) http.Handler {
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(middleware.RequestContext(), middleware.Origin(d.AllowedOrigin))
	api := &handler.API{Auth: d.Auth, Users: d.Users, Conversations: d.Conversations, Settings: d.Settings, SecureCookies: d.SecureCookies}
	r.GET("/health/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "live"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		if d.Ready == nil || d.Ready(c.Request.Context()) != nil {
			c.JSON(503, gin.H{"status": "not_ready"})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})
	v := r.Group("/api/v1")
	authLimit := middleware.RateLimit(10, time.Minute)
	v.POST("/auth/register", authLimit, api.Register)
	v.POST("/auth/login", authLimit, api.Login)
	protected := v.Group("")
	protected.Use(middleware.RequireAuth(d.Auth))
	protected.POST("/auth/logout", api.Logout)
	protected.GET("/me", api.Me)
	protected.GET("/settings", api.GetSettings)
	protected.PATCH("/settings", api.UpdateSettings)
	protected.GET("/conversations", api.ListConversations)
	protected.POST("/conversations", api.CreateConversation)
	protected.PATCH("/conversations/:id", api.RenameConversation)
	protected.DELETE("/conversations/:id", api.DeleteConversation)
	protected.GET("/conversations/:id/messages", api.Messages)
	protected.POST("/conversations/:id/runs", api.SubmitRun)
	protected.GET("/agents", api.Agents)
	return r
}
