package memory

import (
	"context"
	"errors"
	"slices"
	"strings"

	"agent-platform/backend/internal/model"
)

var ErrTravelContextBudget = errors.New("本次旅程内容超过上下文预算，已停止本轮处理。请将旅程分段规划，例如先安排第一段，再处理后续路段。")

type SourceContextResult struct {
	Messages   []model.Message
	Summary    string
	SourceRuns []string
}
type SourceSummarizer func(context.Context, string, []model.Message) (string, error)

// PrepareSourceContext receives only authorized history. It never drops a
// partial group or summarizes the active run to make a request appear to fit.
func PrepareSourceContext(ctx context.Context, history []model.Message, currentRun string, budget int, previous string, covered []string, estimate func(string, []model.Message) int, summarize SourceSummarizer) (SourceContextResult, error) {
	remaining := []model.Message{}
	for _, msg := range history {
		if msg.RunID == currentRun || !slices.Contains(covered, msg.RunID) {
			remaining = append(remaining, msg)
		}
	}
	original := SourceContextResult{Messages: remaining, Summary: previous, SourceRuns: slices.Clone(covered)}
	if budget <= 0 || estimate == nil {
		return original, ErrTravelContextBudget
	}
	trigger := budget * 80 / 100
	if estimate(previous, remaining) < trigger {
		return original, nil
	}
	candidate := original
	for tries := 0; tries < 3 && len(candidate.Messages) > 0 && summarize != nil; tries++ {
		groupID := candidate.Messages[0].RunID
		if groupID == "" || groupID == currentRun {
			break
		}
		cutoff := 1
		for cutoff < len(candidate.Messages) && candidate.Messages[cutoff].RunID == groupID {
			cutoff++
		}
		group := candidate.Messages[:cutoff]
		if !completeSourceTurn(group) {
			break
		}
		summary, err := summarize(ctx, candidate.Summary, group)
		if err != nil {
			if ctx.Err() != nil {
				return original, ctx.Err()
			}
			break
		}
		if strings.TrimSpace(summary) == "" {
			break
		}
		candidate.Summary = strings.TrimSpace(summary)
		candidate.Messages = candidate.Messages[cutoff:]
		if !slices.Contains(candidate.SourceRuns, groupID) {
			candidate.SourceRuns = append(candidate.SourceRuns, groupID)
		}
		if estimate(candidate.Summary, candidate.Messages) <= trigger {
			return candidate, nil
		}
	}
	if estimate(original.Summary, original.Messages) > budget {
		return original, ErrTravelContextBudget
	}
	return original, nil
}
func completeSourceTurn(messages []model.Message) bool {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || len(messages[len(messages)-1].ToolCalls) > 0 {
		return false
	}
	pending, seen := map[string]bool{}, map[string]bool{}
	for _, msg := range messages {
		if msg.Role == "tool" {
			if !pending[msg.ToolCallID] {
				return false
			}
			delete(pending, msg.ToolCallID)
			continue
		}
		if len(pending) > 0 {
			return false
		}
		for _, call := range msg.ToolCalls {
			if call.ID == "" || seen[call.ID] {
				return false
			}
			pending[call.ID] = true
			seen[call.ID] = true
		}
	}
	return len(pending) == 0
}
