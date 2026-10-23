package integration

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPartialOutputSavedOnPause(t *testing.T) {
	f, m, _, a, _ := recoveryFixture(t)
	a.events = []contracts.Event{{Type: "message.delta", Payload: []byte(`{"text":"visible partial"}`)}}
	r := recoveryRun(t, f, m, a)
	if _, e := m.Pause(context.Background(), f.scope, r.ID); e != nil {
		t.Fatal(e)
	}
	got, _ := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if got.PartialOutput != "visible partial" {
		t.Fatalf("partial lost: %+v", got)
	}
	history, _ := f.repo.ListMessages(context.Background(), f.scope, f.conversation, 0, 100)
	found := false
	for _, v := range history {
		found = found || v.Content == "visible partial"
	}
	if !found {
		t.Fatal("partial not in display history")
	}
}

func TestTravelPhaseEvents(t *testing.T) {
	f, ref, _ := travelFixture(t)
	ctx := context.Background()
	valid := contracts.Event{RunID: ref.RunID, Attempt: ref.Attempt, Type: "travel.phase", Payload: []byte(`{"phase":"validating"}`)}
	if err := f.repo.AppendRunEvent(ctx, f.scope, valid); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"phase":"arbitrary"}`, `{"phase":"planning","prompt":"must not persist"}`, `{"phase":"querying","plan_id":"forged"}`} {
		bad := valid
		bad.Payload = []byte(payload)
		if err := f.repo.AppendRunEvent(ctx, f.scope, bad); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("invalid event accepted: %v", err)
		}
	}
	rows, err := f.repo.ListRunEvents(ctx, f.scope, ref.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.Type == "travel.phase" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("unexpected phase event count %d", count)
	}
}
func TestCompletedAnswerAdvancesPartialHistoryCursor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.admit(t)
	f.pool.Exec(ctx, "UPDATE agent_runs SET status='running' WHERE id=$1", r.ID)
	if e := f.repo.SavePartialOutput(ctx, f.scope, r.ID, 1, "partial"); e != nil {
		t.Fatal(e)
	}
	history, _ := f.repo.ListMessages(ctx, f.scope, f.conversation, 0, 100)
	cursor := history[len(history)-1].Ordinal
	if e := f.repo.CompleteRun(ctx, model.FinishRunInput{Scope: f.scope, RunID: r.ID, Attempt: 1, FinalText: "complete"}); e != nil {
		t.Fatal(e)
	}
	later, e := f.repo.ListMessages(ctx, f.scope, f.conversation, cursor, 100)
	if e != nil || len(later) != 1 || later[0].Content != "complete" {
		t.Fatalf("final answer lost after partial cursor: %+v %v", later, e)
	}
}
func TestCompletionPublishedAfterCommit(t *testing.T) {
	f, m, app, a := chatFixture(t)
	r, e := submit(t, f, app, "event")
	if e != nil {
		t.Fatal(e)
	}
	takeInput(t, a)
	ch, stop, e := m.Subscribe(context.Background(), f.scope, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	a.release <- struct{}{}
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no completion notification")
	}
	got, e := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if e != nil || got.Status != "succeeded" {
		t.Fatalf("published before state: %+v %v", got, e)
	}
	events, e := m.Events(context.Background(), f.scope, r.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	if events[len(events)-1].Type != "run.completed" {
		t.Fatal("missing durable completion")
	}
	messages, _ := f.repo.ListMessages(context.Background(), f.scope, f.conversation, 0, 100)
	if messages[len(messages)-1].Content != "answer" {
		t.Fatal("published before answer commit")
	}
}

type failedAgent struct{}
type failedExecution struct{}

func (failedAgent) NewExecution(context.Context, contracts.RunInput) (contracts.Execution, error) {
	return failedExecution{}, nil
}
func (failedExecution) Start(context.Context, func(contracts.Event) error) (contracts.ExecutionResult, error) {
	return contracts.ExecutionResult{}, errors.New("request Authorization Bearer api-secret password-secret")
}
func (failedExecution) Pause(context.Context) (contracts.PauseResult, error) {
	return contracts.PauseResult{}, nil
}
func (failedExecution) Cancel(context.Context) error { return nil }
func TestModelErrorRedacted(t *testing.T) {
	f := setup(t)
	reg := manager.NewRegistry()
	reg.Register("general", failedAgent{})
	m := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{ExecutionTimeout: time.Minute})
	defer m.Close()
	r, e := m.Submit(context.Background(), f.input("fail", "question"), "general")
	if e != nil {
		t.Fatal(e)
	}
	m.Wait()
	got, e := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if e != nil || got.ErrorCode != "execution_failed" {
		t.Fatalf("bad failure: %+v %v", got, e)
	}
	events, e := m.Events(context.Background(), f.scope, r.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "secret") {
			t.Fatal("leaked provider error")
		}
	}
}
func TestExecutionTimeoutDistinct(t *testing.T) {
	f := setup(t)
	a := &pausableAgent{f: f, inputs: make(chan contracts.RunInput, 1)}
	reg := manager.NewRegistry()
	reg.Register("general", a)
	m := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{ExecutionTimeout: 20 * time.Millisecond})
	defer m.Close()
	r, e := m.Submit(context.Background(), f.input("timeout", "question"), "general")
	if e != nil {
		t.Fatal(e)
	}
	m.Wait()
	got, _ := f.repo.GetRun(context.Background(), f.scope, r.ID)
	if got.Status != "timed_out" || got.ErrorCode != "execution_timeout" {
		t.Fatalf("timeout: %+v", got)
	}
}
func streamingRouter(t *testing.T) (http.Handler, *fixture, *manager.Manager, *pausableAgent, *http.Cookie) {
	f, m, l, a, _ := recoveryFixture(t)
	auth := authsvc.New(f.repo)
	_, e := auth.Register(context.Background(), "web-user", "password-123")
	if e != nil {
		t.Fatal(e)
	}
	h := router.New(router.Dependencies{Auth: auth, Users: usersvc.New(f.repo), Conversations: convsvc.New(f.repo), Settings: settingssvc.New(f.repo), Manager: m, Pages: l})
	cookie, _ := webLogin(t, h)
	return h, f, m, a, cookie
}
func TestUnauthorizedSSEDenied(t *testing.T) {
	h, f, m, a, cookie := streamingRouter(t)
	r := recoveryRun(t, f, m, a)
	w := request(h, "GET", "/api/v1/runs/"+r.ID+"/events", "", cookie, "")
	if w.Code != 404 {
		t.Fatalf("foreign events: %d", w.Code)
	}
}
func TestSSEDisconnectDoesNotPause(t *testing.T) {
	h, f, m, a, cookie := streamingRouter(t)
	auth := authsvc.New(f.repo)
	s, e := auth.Authenticate(context.Background(), cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	conv, e := f.repo.CreateConversation(context.Background(), s, "general", "sse")
	if e != nil {
		t.Fatal(e)
	}
	page, e := manager.NewLeaseManager(f.repo, m, time.Now, 20*time.Second).Register(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	s.PageID = page.PageID
	f.scope = s
	f.conversation = conv.ID
	r := recoveryRun(t, f, m, a)
	server := httptest.NewServer(h)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/runs/"+r.ID+"/events", nil)
	req.AddCookie(cookie)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	got, _ := f.repo.GetRun(context.Background(), s, r.ID)
	if got.Status != "running" {
		t.Fatalf("SSE stopped execution: %+v", got)
	}
}
