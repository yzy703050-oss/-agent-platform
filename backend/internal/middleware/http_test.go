package middleware

import (
	"agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedAuth struct{}

func (failedAuth) Authenticate(context.Context, string) (model.Scope, error) {
	return model.Scope{}, errors.New("database password=secret")
}
func (failedAuth) CheckCSRF(context.Context, string, string) error { return nil }
func TestAuthStorageFailureRedacted(t *testing.T) {
	h := gin.New()
	h.Use(RequireAuth(failedAuth{}))
	h.GET("/", func(c *gin.Context) { c.Status(200) })
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cookie", "session=opaque")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unexpected auth failure: %d %s", w.Code, w.Body.String())
	}
}

type csrfStoreFailed struct{}

func (csrfStoreFailed) Authenticate(context.Context, string) (model.Scope, error) {
	return model.Scope{UserID: "user"}, nil
}
func (csrfStoreFailed) CheckCSRF(context.Context, string, string) error {
	return errors.New("database password=secret")
}
func TestCSRFStorageFailureRedacted(t *testing.T) {
	h := gin.New()
	h.Use(RequireAuth(csrfStoreFailed{}))
	h.POST("/", func(c *gin.Context) { c.Status(200) })
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Cookie", "session=opaque")
	r.Header.Set("X-CSRF-Token", "csrf")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("database failure is not forgery: %d %s", w.Code, w.Body.String())
	}
}
