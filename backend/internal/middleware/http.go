package middleware

import (
	"agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"time"
)

type Authenticator interface {
	Authenticate(context.Context, string) (model.Scope, error)
	CheckCSRF(context.Context, string, string) error
}

func Scope(c *gin.Context) model.Scope { return c.MustGet("scope").(model.Scope) }
func Reject(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "message": code, "request_id": c.GetString("request_id")})
}
func RequestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		start := time.Now()
		defer func() {
			if recover() != nil {
				slog.Error("http_panic", "request_id", id)
				Reject(c, 500, "internal_error")
			}
			slog.Info("http_request", "request_id", id, "method", c.Request.Method, "route", c.FullPath(), "status", c.Writer.Status(), "duration_ms", time.Since(start).Milliseconds())
		}()
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	}
}
func Origin(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
			if got := c.GetHeader("Origin"); got != "" && got != origin {
				Reject(c, 403, "origin_rejected")
				return
			}
		}
		c.Next()
	}
}
func RequireAuth(auth Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("session")
		if err != nil {
			Reject(c, 401, "unauthenticated")
			return
		}
		scope, err := auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			if errors.Is(err, model.ErrUnauthenticated) {
				Reject(c, 401, "unauthenticated")
			} else {
				Reject(c, 503, "auth_unavailable")
			}
			return
		}
		c.Set("scope", scope)
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
			if csrfErr := auth.CheckCSRF(c.Request.Context(), token, c.GetHeader("X-CSRF-Token")); csrfErr != nil {
				if errors.Is(csrfErr, model.ErrUnauthenticated) {
					Reject(c, 403, "csrf_rejected")
				} else {
					Reject(c, 503, "auth_unavailable")
				}
				return
			}
		}
		c.Next()
	}
}
