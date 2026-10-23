package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Store interface {
	CreateUser(context.Context, model.User, string) error
	FindAccount(context.Context, string) (model.Account, error)
	CreateSession(context.Context, model.Session) error
	FindSession(context.Context, string) (model.Session, error)
	RevokeSession(context.Context, string) error
}
type Service struct{ store Store }
type LoginResult struct {
	User             model.User
	Token, CSRFToken string
}

func New(store Store) *Service { return &Service{store: store} }
func digest(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func (s *Service) Register(ctx context.Context, username, password string) (model.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 3 || len(username) > 100 || len(password) < 8 || len(password) > 72 {
		return model.User{}, model.ErrInvalidInput
	}
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_.@+-", c)) {
			return model.User{}, model.ErrInvalidInput
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, err
	}
	user := model.User{ID: uuid.NewString(), Username: username}
	return user, s.store.CreateUser(ctx, user, string(hash))
}
func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	account, err := s.store.FindAccount(ctx, strings.ToLower(strings.TrimSpace(username)))
	if errors.Is(err, model.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return LoginResult{}, model.ErrUnauthenticated
	}
	if err != nil {
		return LoginResult{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
		return LoginResult{}, model.ErrUnauthenticated
	}
	raw, csrf := token(), token()
	session := model.Session{ID: uuid.NewString(), UserID: account.User.ID, TokenHash: digest(raw), CSRFHash: digest(csrf), ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
	if err = s.store.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: account.User, Token: raw, CSRFToken: csrf}, nil
}
func (s *Service) Authenticate(ctx context.Context, raw string) (model.Scope, error) {
	if raw == "" {
		return model.Scope{}, model.ErrUnauthenticated
	}
	session, err := s.store.FindSession(ctx, digest(raw))
	if errors.Is(err, model.ErrNotFound) {
		return model.Scope{}, model.ErrUnauthenticated
	}
	if err != nil {
		return model.Scope{}, err
	}
	return model.Scope{UserID: session.UserID, SessionID: session.ID}, nil
}
func (s *Service) CheckCSRF(ctx context.Context, raw, csrf string) error {
	session, err := s.store.FindSession(ctx, digest(raw))
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	if csrf == "" || subtle.ConstantTimeCompare([]byte(session.CSRFHash), []byte(digest(csrf))) != 1 {
		return model.ErrUnauthenticated
	}
	return nil
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	return s.store.RevokeSession(ctx, digest(raw))
}
