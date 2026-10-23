package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"agent-platform/backend/internal/model"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
)

func TestCrossUserConversationAccess(t *testing.T) {
	f := setup(t)
	s := convsvc.New(f.repo)
	scope := f.scope
	scope.UserID = "user-b"
	if _, e := s.RequireAccess(context.Background(), scope, f.conversation); !errors.Is(e, model.ErrNotFound) {
		t.Fatalf("cross user allowed: %v", e)
	}
	if e := s.Rename(context.Background(), scope, f.conversation, "stolen"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
}
func TestDeleteActiveConversationConflict(t *testing.T) {
	f := setup(t)
	f.admit(t)
	s := convsvc.New(f.repo)
	if e := s.Delete(context.Background(), f.scope, f.conversation); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}
func TestStableHistoryCursor(t *testing.T) {
	f := setup(t)
	s := convsvc.New(f.repo)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, e := s.Create(ctx, f.scope, "general", "title"); e != nil {
			t.Fatal(e)
		}
	}
	first, next, e := s.List(ctx, f.scope, "", 2)
	if e != nil || len(first) != 2 || next == "" {
		t.Fatalf("first page: %d %q %v", len(first), next, e)
	}
	second, _, e := s.List(ctx, f.scope, next, 2)
	if e != nil || len(second) != 2 {
		t.Fatalf("second page: %d %v", len(second), e)
	}
	for _, a := range first {
		for _, b := range second {
			if a.ID == b.ID {
				t.Fatal("duplicate cursor boundary")
			}
		}
	}
}
func TestSettingsIsolation(t *testing.T) {
	f := setup(t)
	s := settingssvc.New(f.repo)
	ctx := context.Background()
	in := model.UserSettings{Theme: "dark", ReplyPreference: "concise"}
	if e := s.Update(ctx, f.scope, in); e != nil {
		t.Fatal(e)
	}
	got, e := s.Get(ctx, f.scope)
	if e != nil || got != in {
		t.Fatalf("settings: %+v %v", got, e)
	}
	other, e := s.Get(ctx, model.Scope{UserID: "user-b"})
	if e != nil || other.Theme != "system" || other.ReplyPreference != "" {
		t.Fatalf("leaked settings: %+v %v", other, e)
	}
	if e = s.Update(ctx, f.scope, model.UserSettings{Theme: "bad"}); !errors.Is(e, model.ErrInvalidInput) {
		t.Fatal(e)
	}
}
func TestMessageHistoryCursor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r, e := f.repo.AcceptRun(ctx, f.input(fmt.Sprint("page-", i), fmt.Sprint("question-", i)))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID); e != nil {
			t.Fatal(e)
		}
		if e = f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, FinalText: "answer"}); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	after := int64(0)
	for {
		rows, e := f.repo.ListMessages(ctx, f.scope, f.conversation, after, 2)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) == 0 {
			break
		}
		for _, m := range rows {
			if seen[m.ID] || m.Ordinal <= after {
				t.Fatal("unstable cursor")
			}
			seen[m.ID] = true
			after = m.Ordinal
		}
	}
	if len(seen) != 6 {
		t.Fatalf("lost message: %d", len(seen))
	}
}
