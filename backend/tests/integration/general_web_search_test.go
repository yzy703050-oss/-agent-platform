package integration

import (
	"agent-platform/backend/internal/agent/agents/general"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agenttools"
	"agent-platform/backend/internal/client/websearch"
	pm "agent-platform/backend/internal/model"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type countedSearch struct{ calls atomic.Int32 }

func (s *countedSearch) Search(context.Context, websearch.Request) (websearch.Response, error) {
	s.calls.Add(1)
	return websearch.Response{Status: "ok", Results: []websearch.Result{{Title: "Eino", URL: "https://www.cloudwego.io/docs/eino/", Summary: "官方资料"}}}, nil
}

type searchRecoveryModel struct {
	boundary chan struct{}
	release  chan struct{}
	signaled atomic.Bool
	calls    atomic.Int32
}

func (m *searchRecoveryModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *searchRecoveryModel) Generate(ctx context.Context, in []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls.Add(1)
	searchSeen, settingsSeen := false, false
	for _, msg := range in {
		if msg.Role == schema.Tool {
			if msg.ToolCallID == "search-first" {
				searchSeen = true
			}
			if msg.ToolCallID == "settings-second" {
				settingsSeen = true
			}
		}
	}
	if settingsSeen {
		return schema.AssistantMessage("已完成，来源 https://www.cloudwego.io/docs/eino/", nil), nil
	}
	name, id, args := "web_search", "search-first", `{"query":"Eino"}`
	if searchSeen {
		if m.boundary != nil && m.signaled.CompareAndSwap(false, true) {
			close(m.boundary)
			select {
			case <-m.release:
			case <-ctx.Done():
			}
		}
		name, id, args = "get_my_settings", "settings-second", `{}`
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}}), nil
}
func (m *searchRecoveryModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), err
}

func TestGeneralSearchPersistenceAndRecovery(t *testing.T) {
	t.Run("manager-history", func(t *testing.T) {
		f := setup(t)
		ctx := context.Background()
		search := &countedSearch{}
		settings := &countedSettings{}
		var a *general.Agent
		a = general.NewAgent(&searchRecoveryModel{}, settings, func(in contracts.RunInput) adk.CheckPointStore {
			return &general.RepositoryStore{Store: f.repo, Input: in, Fingerprint: a.Fingerprint()}
		}, "search-test", time.Second, general.WithWebSearch(search))
		reg := manager.NewRegistry()
		reg.Register("general", a)
		mgr := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 8, ExecutionTimeout: 10 * time.Second, ToolTimeout: time.Second})
		defer mgr.Close()
		run, err := mgr.Submit(ctx, f.input("search", "搜索 Eino 然后读取设置"), "general")
		if err != nil {
			t.Fatal(err)
		}
		mgr.Wait()
		final, err := f.repo.GetRun(ctx, f.scope, run.ID)
		if err != nil || final.Status != "succeeded" {
			t.Fatalf("final=%+v err=%v", final, err)
		}
		messages, err := f.repo.ListMessages(ctx, f.scope, f.conversation, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, msg := range messages {
			if msg.Role == "tool" && msg.ToolCallID == "search-first" && strings.Contains(msg.Content, "https://www.cloudwego.io/docs/eino/") {
				found = true
			}
		}
		if !found || search.calls.Load() != 1 {
			t.Fatal("search history missing or replayed")
		}
		events, err := mgr.Events(ctx, f.scope, run.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		completed := 0
		for _, event := range events {
			if event.Type == "tool.completed" && strings.Contains(string(event.Payload), "web_search") {
				completed++
			}
		}
		if completed != 1 {
			t.Fatalf("search events=%d", completed)
		}
		other := f.scope
		other.UserID = "other-user"
		if _, err = f.repo.ListMessages(ctx, other, f.conversation, 0, 100); err == nil {
			t.Fatal("cross-user search history leaked")
		}
	})
	// Exercise the same exported Runner safe-stop boundary as the existing PG recovery test.
	// Do not wait for forced stream cancellation: that is deliberately non-resumable.
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "switch-incompatible"}[changed], func(t *testing.T) {
			f := setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			search := &countedSearch{}
			settings := &countedSettings{}
			first := &searchRecoveryModel{boundary: make(chan struct{}), release: make(chan struct{})}
			fingerprint := func(enabled bool) string {
				opts := []general.Option{}
				if enabled {
					opts = append(opts, general.WithWebSearch(search))
				}
				return general.NewAgent(first, settings, nil, "search-test", time.Second, opts...).Fingerprint()
			}
			kernel := func(m *searchRecoveryModel, enabled bool) adk.Agent {
				tools := []tool.BaseTool{agenttools.NewSettingsTool(settings, f.scope, time.Second)}
				if enabled {
					tools = append(tools, agenttools.NewWebSearchTool(search, time.Second))
				}
				a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "general", Model: m, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}}})
				if err != nil {
					t.Fatal(err)
				}
				return a
			}
			run := f.admit(t)
			run, err := f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: run.ID, Attempt: 1, From: "accepted", To: "running"})
			if err != nil {
				t.Fatal(err)
			}
			store := func(run pm.Run, enabled bool) *general.RepositoryStore {
				return &general.RepositoryStore{Store: f.repo, Input: contracts.RunInput{Scope: f.scope, Run: run}, Fingerprint: fingerprint(enabled)}
			}
			runner, err := general.NewRunner(ctx, general.RunnerConfig{Agent: kernel(first, true), Store: store(run, true), RunID: run.ID, Fingerprint: fingerprint(true), Streaming: true, PauseTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx, []*schema.Message{schema.UserMessage("search and settings")}, nil) }()
			select {
			case <-first.boundary:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err = f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: run.ID, Attempt: 1, From: "running", To: "pausing"}); err != nil {
				t.Fatal(err)
			}
			if !runner.RequestPause() {
				t.Fatal("safe pause not acknowledged")
			}
			close(first.release)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if _, err = f.repo.FinalizePause(ctx, pm.FinalizePauseInput{Scope: f.scope, RunID: run.ID, Attempt: 1}); err != nil {
				t.Fatal(err)
			}
			if search.calls.Load() != 1 || settings.calls.Load() != 0 {
				t.Fatal("tool executed after safe pause")
			}
			// Reading saved state and constructing fresh objects must not run any tool.
			if _, err = f.repo.GetRun(ctx, f.scope, run.ID); err != nil {
				t.Fatal(err)
			}
			freshModel := &searchRecoveryModel{}
			run, err = f.repo.BeginResume(ctx, f.scope, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			run, err = f.repo.TransitionRun(ctx, pm.TransitionRunInput{Scope: f.scope, RunID: run.ID, Attempt: 2, From: "resuming", To: "running"})
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := general.NewRunner(ctx, general.RunnerConfig{Agent: kernel(freshModel, !changed), Store: store(run, !changed), RunID: run.ID, Fingerprint: fingerprint(!changed), Streaming: true, Resume: true})
			if changed {
				if !errors.Is(err, general.ErrCheckpointIncompatible) || freshModel.calls.Load() != 0 || search.calls.Load() != 1 {
					t.Fatalf("incompatible execution: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if freshModel.calls.Load() != 0 {
				t.Fatal("constructing resume executed model")
			}
			if err = resumed.Run(ctx, nil, nil); err != nil {
				t.Fatal(err)
			}
			if search.calls.Load() != 1 || settings.calls.Load() != 1 || resumed.Result().FinalText == "" {
				t.Fatalf("replayed search=%d settings=%d result=%+v", search.calls.Load(), settings.calls.Load(), resumed.Result())
			}
		})
	}
}
