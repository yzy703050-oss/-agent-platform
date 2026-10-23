package integration

import (
	"agent-platform/backend/internal/model"
	authsvc "agent-platform/backend/internal/service/auth"
	"context"
	"errors"
	"testing"
)

func TestLoginLogoutRevokesSession(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	s := authsvc.New(f.repo)
	user, e := s.Register(ctx, " New.User ", "correct-password")
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.Login(ctx, "NEW.USER", "correct-password")
	if e != nil {
		t.Fatal(e)
	}
	if session.User.ID != user.ID || session.CSRFToken == "" || session.Token == "" {
		t.Fatal("invalid login result")
	}
	scope, e := s.Authenticate(ctx, session.Token)
	if e != nil || scope.UserID != user.ID {
		t.Fatalf("authenticate: %+v %v", scope, e)
	}
	if e = s.CheckCSRF(ctx, session.Token, session.CSRFToken); e != nil {
		t.Fatal(e)
	}
	if e = s.CheckCSRF(ctx, session.Token, "forged"); !errors.Is(e, model.ErrUnauthenticated) {
		t.Fatalf("csrf accepted: %v", e)
	}
	if e = s.Logout(ctx, session.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, session.Token); !errors.Is(e, model.ErrUnauthenticated) {
		t.Fatalf("revoked session accepted: %v", e)
	}
}
func TestDuplicateAccount(t *testing.T) {
	f := setup(t)
	s := authsvc.New(f.repo)
	ctx := context.Background()
	if _, e := s.Register(ctx, "account", "password-123"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Register(ctx, " ACCOUNT ", "password-123"); !errors.Is(e, model.ErrConflict) {
		t.Fatalf("duplicate: %v", e)
	}
}
func TestInvalidCredentialsRejected(t *testing.T) {
	f := setup(t)
	s := authsvc.New(f.repo)
	ctx := context.Background()
	if _, e := s.Register(ctx, "account", "password-123"); e != nil {
		t.Fatal(e)
	}
	for _, username := range []string{"account", "missing"} {
		if _, e := s.Login(ctx, username, "wrong-password"); !errors.Is(e, model.ErrUnauthenticated) {
			t.Fatalf("credentials accepted: %v", e)
		}
	}
}
