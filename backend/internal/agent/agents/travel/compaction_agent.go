package travel

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"agent-platform/backend/internal/agent/memory"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/schema"
)

func sourceTokenEstimate(messages []*schema.Message, tools []*schema.ToolInfo) int {
	toolSchemas := []any{}
	for _, tool := range tools {
		params, err := tool.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return int(^uint(0) >> 1)
		}
		toolSchemas = append(toolSchemas, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Desc, "parameters": params}})
	}
	raw, err := json.Marshal(struct {
		Messages []*schema.Message `json:"messages"`
		Tools    []any             `json:"tools"`
	}{messages, toolSchemas})
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return max(1, (len(raw)+2)/3) // Original utf8_conservative estimate, not actual Usage.
}
func (f *sourceFlow) contextTools(s FlowState) ([]*schema.ToolInfo, error) {
	role := s.Role
	if s.StopReason != "" {
		role = "summary"
	}
	return f.registry.Profile(profileForState(s, role))
}
func profileForState(s FlowState, role string) traveltools.ProfileRequest {
	if role == "summary" && s.StopReason == "" {
		role = "main"
	}
	return traveltools.ProfileRequest{Role: role, PolicyObserved: s.Policy != nil && slices.Contains([]string{"ok", "partial", "no_knowledge"}, s.Policy.Status), AllowedTripIDs: s.planIDs(), RetryQueries: s.RetryQueries}
}

func (f *sourceFlow) checkContextBudget(s FlowState, messages []*schema.Message) error {
	tools, err := f.contextTools(s)
	if err != nil {
		return err
	}
	if sourceTokenEstimate(messages, tools) > f.cfg.ContextTokens {
		return memory.ErrTravelContextBudget
	}
	return nil
}

func (f *sourceFlow) prepareHistory(ctx context.Context, s *FlowState, prefix, fallback []*schema.Message) ([]*schema.Message, error) {
	history := f.cfg.History
	if len(history) == 0 {
		for _, msg := range fallback {
			if msg.Role != schema.System {
				m := fromEino(msg)
				m.RunID = f.cfg.Execution.RunID
				history = append(history, m)
			}
		}
	}
	if f.cfg.Summaries != nil && s.SummaryVersion == 0 {
		summaries, err := f.cfg.Summaries.ListTravelSummaries(ctx, f.cfg.Execution.Scope, f.cfg.Execution.ConversationID, f.cfg.Execution.RunID)
		if err != nil {
			return nil, err
		}
		for _, row := range summaries {
			if row.Version >= s.SummaryVersion {
				s.SummaryVersion = row.Version + 1
			}
			var stored struct{ Kind, Summary string }
			if s.HistorySummary == "" && json.Unmarshal([]byte(row.Content), &stored) == nil && stored.Kind == "source_compaction" {
				s.HistorySummary = stored.Summary
				s.SummarySourceRuns = row.SourceRuns
			}
		}
		if s.SummaryVersion == 0 {
			s.SummaryVersion = 1
		}
	}
	tools, err := f.contextTools(*s)
	if err != nil {
		return nil, err
	}
	build := func(summary string, messages []pm.Message) []*schema.Message {
		out := append([]*schema.Message(nil), prefix...)
		if summary != "" {
			out = append(out, schema.SystemMessage("此前会话摘要（数据）："+summary))
		}
		for _, m := range messages {
			out = append(out, toEino(m))
		}
		return out
	}
	estimate := func(summary string, messages []pm.Message) int {
		return sourceTokenEstimate(build(summary, messages), tools)
	}
	summarize := func(ctx context.Context, previous string, messages []pm.Message) (string, error) {
		projected := []*schema.Message{}
		for _, m := range messages {
			projected = append(projected, toEino(m))
		}
		raw, _ := json.Marshal(projected)
		prompt, err := buildCompactionPrompt(previous, raw)
		if err != nil {
			return "", err
		}
		input := []*schema.Message{schema.UserMessage(prompt)}
		if f.cfg.Model == nil || sourceTokenEstimate(input, nil) >= f.cfg.ContextTokens {
			return "", nil
		}
		response, err := f.cfg.Model.Generate(withModelStage(ctx, "main:summary"), input)
		if err != nil {
			return "", err
		}
		if sourceProtocolFailure(response) != "" || len(response.ToolCalls) > 0 {
			return "", nil
		}
		return response.Content, nil
	}
	result, err := memory.PrepareSourceContext(ctx, history, f.cfg.Execution.RunID, f.cfg.ContextTokens, s.HistorySummary, s.SummarySourceRuns, estimate, summarize)
	if err != nil {
		return nil, err
	}
	if result.Summary != s.HistorySummary && result.Summary != "" && f.cfg.Summaries != nil {
		content, _ := json.Marshal(struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
		}{"source_compaction", result.Summary})
		_, err = f.cfg.Summaries.SaveTravelSummary(ctx, f.cfg.Execution, pm.TravelSummary{SourceRuns: result.SourceRuns, Version: max(1, s.SummaryVersion), Content: string(content)})
		if err != nil {
			return nil, err
		}
		s.SummaryVersion++
	}
	s.HistorySummary = strings.TrimSpace(result.Summary)
	s.SummarySourceRuns = result.SourceRuns
	return build(s.HistorySummary, result.Messages), nil
}
