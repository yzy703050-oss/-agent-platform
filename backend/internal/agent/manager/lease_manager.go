package manager

import (
	"agent-platform/backend/internal/model"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"sync"
	"time"
)

type PageStore interface {
	CreatePage(context.Context, model.Scope, model.Page) error
	VerifyPage(context.Context, model.Scope, string, string, time.Time) error
	TouchPage(context.Context, model.Scope, string, string, time.Time, time.Time) error
	ClosePage(context.Context, model.Scope, string, string, time.Time) error
	CloseSessionPages(context.Context, model.Scope, time.Time) error
	ListExpiredPageRuns(context.Context, time.Time) ([]model.ScopedRun, error)
}
type LeaseManager struct {
	store   PageStore
	manager *Manager
	now     func() time.Time
	ttl     time.Duration
}

func NewLeaseManager(s PageStore, m *Manager, now func() time.Time, ttl time.Duration) *LeaseManager {
	return &LeaseManager{s, m, now, ttl}
}
func tokenHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (l *LeaseManager) Register(ctx context.Context, s model.Scope) (model.PageLease, error) {
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return model.PageLease{}, e
	}
	p := model.PageLease{PageID: uuid.NewString(), Token: hex.EncodeToString(raw), ExpiresAt: l.now().Add(l.ttl)}
	e := l.store.CreatePage(ctx, s, model.Page{ID: p.PageID, TokenHash: tokenHash(p.Token), ExpiresAt: p.ExpiresAt})
	return p, e
}
func (l *LeaseManager) Verify(ctx context.Context, s model.Scope, id, token string) error {
	if token == "" {
		return model.ErrNotFound
	}
	return l.store.VerifyPage(ctx, s, id, tokenHash(token), l.now())
}
func (l *LeaseManager) Heartbeat(ctx context.Context, s model.Scope, id, token string) error {
	if token == "" {
		return model.ErrNotFound
	}
	now := l.now()
	return l.store.TouchPage(ctx, s, id, tokenHash(token), now, now.Add(l.ttl))
}
func (l *LeaseManager) Leave(ctx context.Context, s model.Scope, id, token string) error {
	if token == "" {
		return model.ErrNotFound
	}
	if e := l.store.ClosePage(ctx, s, id, tokenHash(token), l.now()); e != nil {
		return e
	}
	return l.Sweep(ctx)
}
func (l *LeaseManager) EndSession(ctx context.Context, s model.Scope) error {
	if e := l.store.CloseSessionPages(ctx, s, l.now()); e != nil {
		return e
	}
	return l.Sweep(ctx)
}
func (l *LeaseManager) Sweep(ctx context.Context) error {
	runs, e := l.store.ListExpiredPageRuns(ctx, l.now())
	if e != nil {
		return e
	}
	var wg sync.WaitGroup
	for _, x := range runs {
		wg.Add(1)
		go func(x model.ScopedRun) { defer wg.Done(); _, _ = l.manager.Pause(ctx, x.Scope, x.Run.ID) }(x)
	}
	wg.Wait()
	return nil
}
func (l *LeaseManager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			_ = l.Sweep(bounded)
			cancel()
		}
	}
}
