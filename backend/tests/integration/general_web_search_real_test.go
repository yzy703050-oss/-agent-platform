package integration

import (
	"agent-platform/backend/internal/agent/agents/general"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/client/llm"
	"agent-platform/backend/internal/client/websearch"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Measurement only: preserve real streaming and count usage once per model request.
type searchCallMetric struct {
	Started    time.Time
	DurationMS int64              `json:"duration_ms"`
	Usage      *schema.TokenUsage `json:"usage"`
}
type searchMeasurement struct {
	mu    sync.Mutex
	calls []*searchCallMetric
}
type searchMeasuringModel struct {
	inner       em.ToolCallingChatModel
	measurement *searchMeasurement
}

func (m *searchMeasuringModel) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	return &searchMeasuringModel{inner: inner, measurement: m.measurement}, err
}
func (m *searchMeasuringModel) begin() *searchCallMetric {
	c := &searchCallMetric{Started: time.Now()}
	m.measurement.mu.Lock()
	m.measurement.calls = append(m.measurement.calls, c)
	m.measurement.mu.Unlock()
	return c
}
func (m *searchMeasuringModel) observe(c *searchCallMetric, msg *schema.Message) {
	m.measurement.mu.Lock()
	defer m.measurement.mu.Unlock()
	c.DurationMS = time.Since(c.Started).Milliseconds()
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	if c.Usage == nil {
		c.Usage = &schema.TokenUsage{}
	}
	c.Usage.PromptTokens = max(c.Usage.PromptTokens, u.PromptTokens)
	c.Usage.CompletionTokens = max(c.Usage.CompletionTokens, u.CompletionTokens)
	c.Usage.TotalTokens = max(c.Usage.TotalTokens, u.TotalTokens)
}
func (m *searchMeasuringModel) Generate(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	c := m.begin()
	out, err := m.inner.Generate(ctx, in, opts...)
	m.observe(c, out)
	return out, err
}
func (m *searchMeasuringModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	c := m.begin()
	reader, err := m.inner.Stream(ctx, in, opts...)
	if err != nil {
		m.observe(c, nil)
		return nil, err
	}
	return schema.StreamReaderWithConvert(reader, func(msg *schema.Message) (*schema.Message, error) { m.observe(c, msg); return msg, nil }), nil
}
func (m *searchMeasurement) report() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	reported := 0
	sum := schema.TokenUsage{}
	for _, c := range m.calls {
		if c.Usage != nil {
			reported++
			sum.PromptTokens += c.Usage.PromptTokens
			sum.CompletionTokens += c.Usage.CompletionTokens
			sum.TotalTokens += c.Usage.TotalTokens
		}
	}
	var tokens any
	if reported > 0 {
		tokens = map[string]int{"input": sum.PromptTokens, "output": sum.CompletionTokens, "total": sum.TotalTokens}
	}
	return map[string]any{"model_calls": len(m.calls), "usage_reported_calls": reported, "usage_complete": reported == len(m.calls), "tokens": tokens}
}

type usageFixture struct{}

func (usageFixture) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return usageFixture{}, nil
}
func (usageFixture) Generate(context.Context, []*schema.Message, ...em.Option) (*schema.Message, error) {
	return schema.AssistantMessage("unused", nil), nil
}
func (usageFixture) Stream(context.Context, []*schema.Message, ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	chunk := func(content string) *schema.Message {
		msg := schema.AssistantMessage(content, nil)
		msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}
		return msg
	}
	return schema.StreamReaderFromArray([]*schema.Message{chunk("a"), chunk("b")}), nil
}
func TestSearchMetricsStreamCountsUsageOnce(t *testing.T) {
	m := &searchMeasuringModel{inner: usageFixture{}, measurement: &searchMeasurement{}}
	reader, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	joined, err := schema.ConcatMessageStream(reader)
	if err != nil || joined.Content != "ab" {
		t.Fatal("measurement changed stream", err)
	}
	report := m.measurement.report()
	if report["model_calls"] != 1 || report["tokens"].(map[string]int)["total"] != 12 {
		t.Fatal("double counted stream usage", report)
	}
}

func TestWebSearchLive(t *testing.T) {
	if os.Getenv("RUN_WEB_SEARCH_LIVE") != "1" {
		t.Skip("opt-in live DuckDuckGo search")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := websearch.NewWithProxy(ctx, os.Getenv("WEB_SEARCH_PROXY_URL"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := cli.Search(ctx, websearch.Request{Query: "CloudWeGo Eino official documentation"})
	t.Logf("public_search duration_ms=%d status=%s results=%d error=%v", time.Since(started).Milliseconds(), response.Status, len(response.Results), err)
	if err != nil || response.Status != "ok" || len(response.Results) == 0 {
		t.Fatal("live search did not return verifiable sources")
	}
	for _, result := range response.Results {
		t.Logf("source=%s", result.URL)
	}
}

type measuredSearch struct {
	mu      sync.Mutex
	client  *websearch.Client
	fail    bool
	calls   int
	sources []string
}

func (s *measuredSearch) Search(ctx context.Context, r websearch.Request) (websearch.Response, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.fail {
		return websearch.Response{}, websearch.ErrUnavailable
	}
	response, err := s.client.Search(ctx, r)
	s.mu.Lock()
	for _, r := range response.Results {
		s.sources = append(s.sources, r.URL)
	}
	s.mu.Unlock()
	return response, err
}
func TestGeneralSearchRealModel(t *testing.T) {
	if os.Getenv("RUN_GENERAL_SEARCH_REAL") != "1" {
		t.Skip("opt-in real general model + web search")
	}
	for _, tc := range []struct {
		name, prompt string
		fail         bool
	}{
		{"public-search", "请联网搜索 CloudWeGo Eino 官方文档，说明它是什么，并给出搜索返回的来源链接。只需搜索一次；搜索失败就如实说明，不要重试。", false},
		{"direct-reply", "你好，只回复：你好。无需搜索。", false},
		{"failure-feedback", "请联网搜索 Eino 官方文档。如果搜索工具反馈不可用，直接说明搜索失败，不重试、不引用猜测的来源。", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
			defer cancel()
			f := setup(t)
			inner, err := llm.New(ctx, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_API_KEY"))
			if err != nil {
				t.Fatal("model not configured")
			}
			measurement := &searchMeasurement{}
			m := &searchMeasuringModel{inner: inner, measurement: measurement}
			client, err := websearch.NewWithProxy(ctx, os.Getenv("WEB_SEARCH_PROXY_URL"))
			if err != nil {
				t.Fatal(err)
			}
			search := &measuredSearch{client: client, fail: tc.fail}
			var a *general.Agent
			a = general.NewAgent(m, &countedSettings{}, func(in contracts.RunInput) adk.CheckPointStore {
				return &general.RepositoryStore{Store: f.repo, Input: in, Fingerprint: a.Fingerprint()}
			}, "real-search", 5*time.Second, general.WithWebSearch(search))
			reg := manager.NewRegistry()
			reg.Register("general", a)
			mgr := manager.New(f.repo, memory.New(f.repo, 32000), reg, contracts.Limits{Iterations: 4, ToolTimeout: 10 * time.Second, ExecutionTimeout: 100 * time.Second, PauseTimeout: 5 * time.Second})
			defer mgr.Close()
			started := time.Now()
			run, err := mgr.Submit(ctx, f.input("real-search", tc.prompt), "general")
			if err != nil {
				t.Fatal("submission failed")
			}
			mgr.Wait()
			final, err := f.repo.GetRun(ctx, f.scope, run.ID)
			if err != nil {
				t.Fatal("run read failed")
			}
			report := measurement.report()
			report["case"] = tc.name
			report["model"] = os.Getenv("LLM_MODEL")
			report["elapsed_ms"] = time.Since(started).Milliseconds()
			report["status"] = final.Status
			report["error_code"] = final.ErrorCode
			report["answer"] = final.PartialOutput
			search.mu.Lock()
			report["search_calls"] = search.calls
			report["sources"] = append([]string{}, search.sources...)
			search.mu.Unlock()
			encoded, _ := json.Marshal(report)
			t.Log("WEB_SEARCH_METRICS " + string(encoded))
			if final.Status != "succeeded" || final.PartialOutput == "" {
				t.Fatal("real model run did not complete")
			}
			switch tc.name {
			case "public-search":
				linked := false
				for _, source := range search.sources {
					if strings.Contains(final.PartialOutput, source) {
						linked = true
					}
				}
				if search.calls < 1 || len(search.sources) == 0 || !linked {
					t.Error("public search not accepted: no verified source in final answer")
				}
			case "direct-reply":
				if search.calls != 0 {
					t.Error("direct reply unexpectedly searched")
				}
			case "failure-feedback":
				if search.calls != 1 || strings.Contains(final.PartialOutput, "https://") {
					t.Error("search failure not handled without fabricated source")
				}
			}
		})
	}
}
