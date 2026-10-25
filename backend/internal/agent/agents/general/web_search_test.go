package general

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/client/websearch"
	pm "agent-platform/backend/internal/model"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type flowSearch struct {
	calls int
	fail  bool
}

func (s *flowSearch) Search(context.Context, websearch.Request) (websearch.Response, error) {
	s.calls++
	if s.fail {
		return websearch.Response{}, websearch.ErrUnavailable
	}
	return websearch.Response{Status: "ok", Results: []websearch.Result{{Title: "Eino", URL: "https://www.cloudwego.io/docs/eino/", Summary: "official docs"}}}, nil
}

type searchModel struct {
	t       *testing.T
	calls   int
	tools   []*schema.ToolInfo
	direct  bool
	enabled bool
	failed  bool
}

func (m *searchModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.tools = tools
	return m, nil
}
func (m *searchModel) Generate(_ context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.calls++
	m.tools = model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...).Tools
	found := false
	for _, info := range m.tools {
		if info.Name == "web_search" {
			found = true
		}
	}
	if found != m.enabled {
		m.t.Fatalf("search enabled=%v tools=%v", m.enabled, m.tools)
	}
	prompt := ""
	for _, msg := range messages {
		if msg.Role == schema.System {
			prompt += msg.Content
		}
	}
	if strings.Contains(prompt, "web_search") != m.enabled {
		m.t.Fatalf("wrong conditional prompt: %s", prompt)
	}
	if m.direct {
		return schema.AssistantMessage("你好", nil), nil
	}
	for _, msg := range messages {
		if msg.Role == schema.Tool {
			want := `"status":"ok"`
			if m.failed {
				want = `"status":"search_unavailable"`
			}
			if !strings.Contains(msg.Content, want) {
				m.t.Fatalf("tool observation missing: %s", msg.Content)
			}
			if m.failed {
				return schema.AssistantMessage("此次搜索未成功，暂时无法核对来源。", nil), nil
			}
			return schema.AssistantMessage("参考官方文档 https://www.cloudwego.io/docs/eino/", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "search-1", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"Eino 官方文档"}`}}}), nil
}
func (m *searchModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	v, e := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), e
}

func TestGeneralWebSearchFlow(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure-feedback"}[fail], func(t *testing.T) {
			search := &flowSearch{fail: fail}
			m := &searchModel{t: t, enabled: true, failed: fail}
			a := NewAgent(m, &readSettings{}, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "test", time.Second, WithWebSearch(search))
			exec, err := a.NewExecution(context.Background(), contracts.RunInput{Run: pm.Run{ID: "search"}, Limits: contracts.Limits{Iterations: 4, ToolTimeout: time.Second}, Messages: []pm.Message{{Role: "user", Content: "搜索 Eino 文档并给来源"}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := exec.Start(context.Background(), nil)
			if err != nil || result.FinalText == "" || m.calls != 2 || search.calls != 1 {
				t.Fatalf("result=%+v model=%d search=%d err=%v", result, m.calls, search.calls, err)
			}
		})
	}
}
func TestGeneralWebSearchDisabledAndDirectReply(t *testing.T) {
	fingerprints := map[bool]string{}
	for _, enabled := range []bool{false, true} {
		search := &flowSearch{}
		m := &searchModel{t: t, direct: true, enabled: enabled}
		opts := []Option{}
		if enabled {
			opts = append(opts, WithWebSearch(search))
		}
		a := NewAgent(m, &readSettings{}, func(contracts.RunInput) adkStore { return &diskStore{dir: t.TempDir()} }, "same", time.Second, opts...)
		fingerprints[enabled] = a.Fingerprint()
		exec, err := a.NewExecution(context.Background(), contracts.RunInput{Run: pm.Run{ID: "direct"}, Limits: contracts.Limits{Iterations: 2, ToolTimeout: time.Second}, Messages: []pm.Message{{Role: "user", Content: "你好"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = exec.Start(context.Background(), nil); err != nil || search.calls != 0 || m.calls != 1 {
			t.Fatalf("direct calls=%d err=%v", search.calls, err)
		}
	}
	if fingerprints[false] == fingerprints[true] {
		t.Fatal("checkpoint fingerprints did not isolate tool configuration")
	}
}
