package integration

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/model"
	pg "agent-platform/backend/internal/repository/postgres"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type eventReadGate struct {
	*pg.Repository
	entered, release chan struct{}
	once             atomic.Bool
}

func (g *eventReadGate) ListRunEvents(ctx context.Context, s model.Scope, id string, after int64) ([]contracts.Event, error) {
	if g.once.CompareAndSwap(false, true) {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.Repository.ListRunEvents(ctx, s, id, after)
}
func TestSSEReplayConcurrentCompletionOnce(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	gate := &eventReadGate{Repository: f.repo, entered: make(chan struct{}), release: make(chan struct{})}
	a := &heldAgent{inputs: make(chan contracts.RunInput, 1), release: make(chan struct{})}
	reg := manager.NewRegistry()
	reg.Register("general", a)
	m := manager.New(gate, memory.New(f.repo, 32000), reg, contracts.Limits{ExecutionTimeout: time.Minute})
	t.Cleanup(func() { close(a.release); m.Close() })
	auth := authsvc.New(f.repo)
	if _, e := auth.Register(ctx, "web-user", "password-123"); e != nil {
		t.Fatal(e)
	}
	h := router.New(router.Dependencies{Auth: auth, Conversations: convsvc.New(f.repo), Manager: m})
	cookie, _ := webLogin(t, h)
	s, e := auth.Authenticate(ctx, cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.repo.CreateConversation(ctx, s, "general", "race")
	if e != nil {
		t.Fatal(e)
	}
	page, e := manager.NewLeaseManager(f.repo, m, time.Now, 20*time.Second).Register(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	s.PageID = page.PageID
	r, e := m.Submit(ctx, model.AdmitRunInput{Scope: s, ConversationID: c.ID, Content: "question", IdempotencyKey: "race", RequestHash: "question"}, "general")
	if e != nil {
		t.Fatal(e)
	}
	takeInput(t, a)
	server := httptest.NewServer(h)
	defer server.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/runs/"+r.ID+"/events", nil)
	req.AddCookie(cookie)
	responses := make(chan *http.Response, 1)
	failures := make(chan error, 1)
	go func() {
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			failures <- e
			return
		}
		responses <- response
	}()
	select {
	case <-gate.entered:
	case e := <-failures:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Completion commits after subscription, before the first history read; both paths see it.
	a.release <- struct{}{}
	m.Wait()
	close(gate.release)
	var response *http.Response
	select {
	case response = <-responses:
	case e := <-failures:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	seen := map[int64]bool{}
	completed := 0
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == ": heartbeat" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event contracts.Event
		if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); e != nil {
			t.Fatal(e)
		}
		if seen[event.Seq] {
			t.Fatalf("duplicate durable event %d", event.Seq)
		}
		seen[event.Seq] = true
		if event.Type == "run.completed" {
			completed++
		}
	}
	if e = scanner.Err(); e != nil {
		t.Fatal(e)
	}
	if completed != 1 || len(seen) != 3 {
		t.Fatalf("missing or duplicate replay: completed=%d events=%d", completed, len(seen))
	}
}
