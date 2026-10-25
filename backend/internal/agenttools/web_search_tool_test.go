package agenttools

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/client/websearch"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type searchFunc func(context.Context, websearch.Request) (websearch.Response, error)

func (f searchFunc) Search(ctx context.Context, r websearch.Request) (websearch.Response, error) {
	return f(ctx, r)
}
func TestWebSearchToolContractAndFeedback(t *testing.T) {
	calls := 0
	search := searchFunc(func(ctx context.Context, r websearch.Request) (websearch.Response, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing tool deadline")
		}
		if r.Query != "Eino" {
			t.Fatalf("not trimmed: %q", r.Query)
		}
		return websearch.Response{Status: "ok", Results: []websearch.Result{{Title: "Eino", URL: "https://example.org", Summary: "docs"}}}, nil
	})
	tool := NewWebSearchTool(search, time.Second)
	info, err := tool.Info(context.Background())
	if err != nil || info.Name != "web_search" {
		t.Fatal("tool metadata", err)
	}
	for _, arg := range []string{`{}`, `null`, `{"query":""}`, `{"query":"Eino","other":1}`, `{"query":3}`, `{"query":"Eino","time_range":null}`, `{"query":"Eino","time_range":"today"}`, `{"query":"Eino"} {}`, `{"query":"` + strings.Repeat("长", 501) + `"}`} {
		got, err := tool.InvokableRun(context.Background(), arg)
		if err != nil || !strings.Contains(got, `"status":"invalid_arguments"`) || calls != 0 {
			t.Fatalf("args=%s got=%s calls=%d err=%v", arg, got, calls, err)
		}
	}
	var events []contracts.Event
	ctx := WithObserver(context.Background(), func(e contracts.Event) error { events = append(events, e); return nil })
	got, err := tool.InvokableRun(ctx, `{"query":" Eino ","time_range":"w"}`)
	if err != nil || calls != 1 || !strings.Contains(got, `"status":"ok"`) || len(events) != 2 || events[1].Type != "tool.completed" {
		t.Fatalf("got=%s events=%v err=%v", got, events, err)
	}
	var payload map[string]any
	_ = json.Unmarshal(events[1].Payload, &payload)
	if payload["name"] != "web_search" || payload["duration_ms"] == nil || payload["status"] != "ok" {
		t.Fatal("missing event fields")
	}
	for _, tc := range []struct {
		err    error
		status string
	}{{websearch.ErrUnavailable, "search_unavailable"}, {context.DeadlineExceeded, "search_timeout"}} {
		events = nil
		tool := NewWebSearchTool(searchFunc(func(context.Context, websearch.Request) (websearch.Response, error) {
			return websearch.Response{}, tc.err
		}), time.Second)
		got, err := tool.InvokableRun(ctx, `{"query":"Eino"}`)
		if err != nil || !strings.Contains(got, tc.status) || events[1].Type != "tool.failed" {
			t.Fatalf("feedback=%s events=%v err=%v", got, events, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.InvokableRun(cancelled, `{"query":"Eino"}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancel swallowed", err)
	}
	storageErr := errors.New("storage unavailable")
	_, err = tool.InvokableRun(WithObserver(context.Background(), func(contracts.Event) error { return storageErr }), `{"query":"Eino"}`)
	if !errors.Is(err, storageErr) {
		t.Fatal("event failure swallowed")
	}
	late := NewWebSearchTool(searchFunc(func(ctx context.Context, _ websearch.Request) (websearch.Response, error) {
		<-ctx.Done()
		return websearch.Response{Status: "ok"}, nil
	}), time.Millisecond)
	got, err = late.InvokableRun(context.Background(), `{"query":"Eino"}`)
	if err != nil || !strings.Contains(got, "search_timeout") {
		t.Fatal("accepted late success", got, err)
	}
}
