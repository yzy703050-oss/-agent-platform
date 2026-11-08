package runtimeintegration

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/repository/platformpg"
	"agent-platform/backend/internal/service/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestControlClientTransactionBoundaries(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	client := control.Client{}
	tx, err := f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := client.IssueScope(ctx, tx, control.ScopeInput{ScopeID: "scope", OwnerService: "platform", SubjectID: "user", SourceRunID: "run", ConversationID: "conversation", ContextEpoch: 1, ExpiresAt: time.Now().UTC().Add(time.Minute), Capabilities: []string{"model", "commit"}, Targets: []string{"account", "plan"}, SourceScope: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	ref := runtimev1.ExecRef{ScopeID: scope.ScopeID, ScopeHash: scope.ScopeHash, OwnerKind: "job", OwnerID: "job", Lease: runtimev1.LeaseRef{Epoch: 1}}
	if scope.IssuerService != "platform" || scope.SourceRunID != "run" || len(scope.ScopeHash) != 64 {
		t.Fatalf("invalid scope projection: %+v", scope)
	}
	if _, err = client.AssertScope(ctx, tx, ref, "commit", "plan", "commit"); err != nil {
		t.Fatal(err)
	}
	// Admission must use a separate transaction: its first Run lock is UPDATE.
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	a, err := client.ReserveAttempt(ctx, tx, control.AttemptInput{Ref: ref, AttemptID: "a", LogicalKey: "model:first", AttemptNo: 1, Capability: "model", Target: "account", ResourceKeys: []string{"endpoint", "account"}, Reservation: control.Reservation{ModelAttempts: 1, TokenUpperBound: 50, CostUpperMicros: 10}, HoldUntil: time.Now().UTC().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if a.AttemptID != "a" || a.Replayed || a.Reservation.TokenUpperBound != 50 {
		t.Fatalf("bad attempt: %+v", a)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM control.outbound_calls").Scan(&count); err != nil || count != 0 {
		t.Fatalf("reservation escaped transaction: %d %v", count, err)
	}
}

func TestAtomicSessionAndGuard(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	repo := platformpg.New(f.Platform)
	// This is also a compile-time contract with the existing AuthService.
	var store auth.Store = repo
	if err := store.CreateUser(ctx, model.User{ID: "user", Username: "alice"}, "hash"); err != nil {
		t.Fatal(err)
	}
	s := model.Session{ID: "session", UserID: "user", TokenHash: "token", CSRFHash: "csrf", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	otherPool, err := pgxpool.NewWithConfig(ctx, f.Platform.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	otherAPI := platformpg.New(otherPool)
	got, err := otherAPI.FindSession(ctx, "token")
	if err != nil || got.ID != "session" {
		t.Fatalf("cross-instance session: %+v %v", got, err)
	}
	bad := s
	bad.ID = "orphan"
	bad.UserID = "missing"
	if err = store.CreateSession(ctx, bad); err == nil {
		t.Fatal("invalid session accepted")
	}
	var orphan int
	if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM control.session_guards WHERE origin_session_id='orphan'").Scan(&orphan); err != nil || orphan != 0 {
		t.Fatalf("orphan guard: %d %v", orphan, err)
	}
	if err = store.RevokeSession(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err = otherAPI.FindSession(ctx, "token"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("revoked authentication: %v", err)
	}
	var revoked bool
	if err = f.Admin.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM control.session_guards WHERE origin_session_id='session'").Scan(&revoked); err != nil || !revoked {
		t.Fatalf("guard not revoked: %v %v", revoked, err)
	}
	denied(t, f.Platform, "UPDATE platform.sessions SET revoked_at=NULL")
	denied(t, f.Maintenance, "SELECT platform.revoke_session('token')")
	if err = store.RevokeSession(ctx, "unknown"); err != nil {
		t.Fatalf("unknown logout not idempotent: %v", err)
	}
}

func TestControlGuardLifecycleAndBackgroundScope(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	repo := platformpg.New(f.Platform)
	client := control.Client{}
	if err := repo.CreateUser(ctx, model.User{ID: "user", Username: "alice"}, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(ctx, model.Session{ID: "session", UserID: "user", TokenHash: "token", CSRFHash: "csrf", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Platform.Exec(ctx, "SELECT control.set_context_guard('user','conversation',0,1,true)"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Platform.Exec(ctx, `SELECT control.register_run_guard('run','user','conversation','session',clock_timestamp()+interval '5 minutes','{}')`); err == nil {
		t.Fatal("unbounded run accepted")
	}
	if _, err := f.Platform.Exec(ctx, `SELECT control.register_run_guard('run','user','conversation','session',clock_timestamp()+interval '5 minutes','{"model_attempts":2,"tool_attempts":2,"token_upper_bound":100,"cost_upper_micros":0}')`); err != nil {
		t.Fatal(err)
	}
	foreground := issueTestScope(t, f, "foreground", "travel")
	if _, err := f.Platform.Exec(ctx, "SELECT control.close_run_guard('run')"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Travel.Exec(ctx, "SELECT control.assert_scope($1,$2,'commit','plan','commit')", foreground["scope_id"], foreground["scope_hash"]); err == nil {
		t.Fatal("closed run advanced")
	}
	if err := repo.RevokeSession(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	tx, err := f.Maintenance.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := client.IssueScope(ctx, tx, control.ScopeInput{ScopeID: "background", OwnerService: "maintenance", SubjectID: "user", ConversationID: "conversation", OperationID: "operation", ContextEpoch: 1, ExpiresAt: time.Now().Add(time.Minute), Capabilities: []string{"model", "commit"}, Targets: []string{"memory", "account"}, SourceScope: []byte(`{}`), BudgetLimits: control.Reservation{ModelAttempts: 1, TokenUpperBound: 20}})
	if err != nil {
		t.Fatalf("background borrowed closed foreground authority: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Admin.Exec(ctx, "INSERT INTO control.resource_limits(resource_key,max_concurrency) VALUES('account',1)"); err != nil {
		t.Fatal(err)
	}
	tx, err = f.Maintenance.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	ref := runtimev1.ExecRef{ScopeID: scope.ScopeID, ScopeHash: scope.ScopeHash, OwnerKind: "operation", OwnerID: "operation", Lease: runtimev1.LeaseRef{Epoch: 1}}
	if _, err = client.ReserveAttempt(ctx, tx, control.AttemptInput{Ref: ref, AttemptID: "background-call", LogicalKey: "review", AttemptNo: 1, Capability: "model", Target: "account", ResourceKeys: []string{"account"}, Reservation: control.Reservation{ModelAttempts: 1, TokenUpperBound: 20}, HoldUntil: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Platform.Exec(ctx, "SELECT control.set_context_guard('user','conversation',1,2,true)"); err != nil {
		t.Fatal(err)
	}
	tx, err = f.Maintenance.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = client.AssertScope(ctx, tx, ref, "commit", "memory", "commit"); err == nil {
		t.Fatal("cleared context revived")
	}
	tx.Rollback(ctx)
	tx, err = f.Maintenance.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = client.SettleAttempt(ctx, tx, control.Settlement{AttemptID: "background-call", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestControlRejectsInfiniteAuthorityDeadlines(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	t.Run("session", func(t *testing.T) {
		if _, err := f.Platform.Exec(ctx, "SELECT control.register_session_guard('session','user','infinity')"); err == nil {
			t.Fatal("unbounded Session authority accepted")
		}
	})
	if _, err := f.Platform.Exec(ctx, "SELECT control.set_context_guard('user','conversation',0,1,true)"); err != nil {
		t.Fatal(err)
	}
	t.Run("background", func(t *testing.T) {
		if _, err := f.Maintenance.Exec(ctx, `SELECT control.issue_scope('{"scope_id":"scope","owner_service":"maintenance","subject_id":"user","conversation_id":"conversation","operation_id":"operation","context_epoch":1,"expires_at":"infinity","capabilities":["model"],"targets":["account"],"source_scope":{},"budget_limits":{"model_attempts":1,"tool_attempts":0,"token_upper_bound":10,"cost_upper_micros":0}}')`); err == nil {
			t.Fatal("unbounded background authority accepted")
		}
	})
}

func TestControlRunDeadlineIndependentOfSessionExpiry(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	ctx := context.Background()
	if _, err := f.Platform.Exec(ctx, "SELECT control.register_session_guard('session','user',clock_timestamp()+interval '10 seconds')"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Platform.Exec(ctx, "SELECT control.set_context_guard('user','conversation',0,1,true)"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Platform.Exec(ctx, `SELECT control.register_run_guard('run','user','conversation','session',clock_timestamp()+interval '5 minutes','{"model_attempts":2,"tool_attempts":2,"token_upper_bound":100,"cost_upper_micros":0}')`); err != nil {
		t.Fatalf("valid Session must allow a fixed Run deadline: %v", err)
	}
	scope := issueTestScope(t, f, "scope", "travel")
	if _, err := f.Admin.Exec(ctx, "UPDATE control.session_guards SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Travel.Exec(ctx, "SELECT control.assert_scope($1,$2,'commit','plan','commit')", scope["scope_id"], scope["scope_hash"]); err == nil {
		t.Fatal("longer Run deadline bypassed original Session expiry")
	}
}
