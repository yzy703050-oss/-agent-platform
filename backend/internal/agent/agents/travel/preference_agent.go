package travel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	pm "agent-platform/backend/internal/model"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type preferenceHistory interface {
	ReadAnalysisHistory(context.Context, pm.TravelExecution) ([]pm.TripPlan, error)
}
type preferenceMemory interface {
	Update(context.Context, pm.TravelExecution, string, pm.TravelMemoryCommand) (pm.TravelMemoryResult, error)
	ReadEffective(context.Context, pm.Scope, string) ([]pm.TravelPreference, error)
	SaveAnalysis(context.Context, pm.TravelExecution, string, []pm.TravelMemoryUpdate, pm.TravelPreferenceAnalysis, []string) (pm.TravelMemoryResult, error)
}
type PreferenceAgent struct {
	model   em.BaseChatModel
	history preferenceHistory
	memory  preferenceMemory
}

func (a *PreferenceAgent) Update(ctx context.Context, ref pm.TravelExecution, id string, cmd pm.TravelMemoryCommand) (pm.TravelMemoryResult, error) {
	if cmd.Operation == "reanalyze" {
		return a.analyze(ctx, ref, id, true)
	}
	return a.memory.Update(ctx, ref, id, cmd)
}
func (a *PreferenceAgent) Maintain(ctx context.Context, ref pm.TravelExecution) error {
	_, err := a.analyze(ctx, ref, "", false)
	return err
}

func selectPreferenceSamples(history []pm.TripPlan, covered []string, force bool) []pm.TripPlan {
	selected := []pm.TripPlan{}
	seen := map[string]bool{}
	for _, p := range history {
		if p.ID == "" || seen[p.ID] || !p.UserConfirmed || p.Status != "completed" || p.Validation.Status != "complete" || slices.Contains([]string{"paused", "cancelled", "error", "workflow_execution_error"}, p.StopReason) || !force && slices.Contains(covered, p.ID) {
			continue
		}
		simulated := false
		for _, task := range p.Tasks {
			if task.Train != nil && task.Train.Candidate.IsDemo || task.Hotel != nil && task.Hotel.Candidate.IsDemo {
				simulated = true
			}
		}
		if simulated {
			continue
		}
		seen[p.ID] = true
		selected = append(selected, p)
	}
	return selected
}
func (a *PreferenceAgent) analyze(ctx context.Context, ref pm.TravelExecution, id string, force bool) (pm.TravelMemoryResult, error) {
	empty := pm.TravelMemoryResult{Applied: []pm.TravelMemoryApplied{}, Rejected: []pm.TravelMemoryUpdate{}}
	history, err := a.history.ReadAnalysisHistory(ctx, ref)
	if err != nil {
		return empty, err
	}
	rows, err := a.memory.ReadEffective(ctx, ref.Scope, ref.RunID)
	if err != nil {
		return empty, err
	}
	analysis := pm.TravelPreferenceAnalysis{}
	for _, row := range rows {
		if row.Key == "$analysis" {
			if err = json.Unmarshal([]byte(row.Value), &analysis); err != nil {
				return empty, err
			}
		}
	}
	samples := selectPreferenceSamples(history, analysis.CoveredTripIDs, force)
	if len(samples) == 0 {
		empty.Status = "no_samples"
		empty.Reason = "没有已确认的有效历史行程。"
		return empty, nil
	}
	if !force && len(samples) < 3 {
		return empty, nil
	}
	ids, sources := []string{}, []string{}
	projected := []any{}
	for _, p := range samples {
		ids = append(ids, p.ID)
		if !slices.Contains(sources, p.SourceRunID) {
			sources = append(sources, p.SourceRunID)
		}
		tasks := []any{}
		for _, t := range p.Tasks {
			tasks = append(tasks, map[string]any{"id": t.ID, "origin": t.Origin, "destination": t.Destination, "purpose": t.Purpose, "conditions": t.Conditions, "content": t.Activities, "train": t.Train, "hotel": t.Hotel})
		}
		projected = append(projected, map[string]any{"trip_plan_id": p.ID, "status": p.Status, "tasks": tasks, "user_confirmed": true})
	}
	memories := []any{}
	owned := []pm.TravelPreference{}
	for _, row := range rows {
		if !strings.HasPrefix(row.Key, "memory_") {
			continue
		}
		source, status := "explicit", "active"
		if row.Origin == "inferred" {
			source = "history"
		}
		if row.Retracted {
			status = "retracted"
		}
		memories = append(memories, map[string]any{"id": row.Key, "text": row.Value, "source": source, "status": status})
		if row.Origin == "inferred" && !row.Retracted && slices.Contains(analysis.MemoryIDs, row.Key) {
			owned = append(owned, row)
		}
	}
	prompt, err := buildPreferencePrompt(mustMarshal(projected), analysis.Summary, mustMarshal(memories))
	if err != nil {
		return empty, err
	}
	response, err := a.model.Generate(withModelStage(ctx, "agent:preference_analysis"), []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, &pm.TravelRuleError{Code: "PREFERENCE_ANALYSIS_FAILED", Message: "未取得有效偏好分析结果，尚未更新记忆。"}
	}
	if sourceProtocolFailure(response) != "" || len(response.ToolCalls) > 0 {
		return empty, pm.ErrInvalidInput
	}
	var result struct {
		Memories []struct {
			Text        string   `json:"text"`
			EvidenceIDs []string `json:"evidence_trip_ids"`
		} `json:"memories"`
		Covered []string `json:"covered_trip_ids"`
	}
	if parseAuxiliaryJSON(response.Content, &result) != nil || result.Memories == nil || result.Covered == nil {
		return empty, pm.ErrInvalidInput
	}
	covered := slices.Clone(result.Covered)
	slices.Sort(covered)
	covered = slices.Compact(covered)
	expected := slices.Clone(ids)
	slices.Sort(expected)
	if !slices.Equal(covered, expected) {
		return empty, pm.ErrInvalidInput
	}
	updates := []pm.TravelMemoryUpdate{}
	for i, item := range result.Memories {
		if strings.TrimSpace(item.Text) == "" || len(item.EvidenceIDs) == 0 {
			return empty, pm.ErrInvalidInput
		}
		for _, evidence := range item.EvidenceIDs {
			if !slices.Contains(ids, evidence) {
				return empty, pm.ErrInvalidInput
			}
		}
		text := item.Text
		update := pm.TravelMemoryUpdate{Op: "add", Text: &text, Source: "history"}
		if i < len(owned) {
			key := owned[i].Key
			update.Op = "replace"
			update.ID = &key
		}
		updates = append(updates, update)
	}
	if len(result.Memories) > 0 {
		for _, row := range owned[min(len(owned), len(result.Memories)):] {
			key := row.Key
			updates = append(updates, pm.TravelMemoryUpdate{Op: "retract", ID: &key, Source: "history"})
		}
	}
	trigger := "bootstrap"
	if analysis.InitialSummaryDone {
		trigger = "incremental"
	}
	if force {
		trigger = "user_requested"
	}
	if id == "" {
		raw, _ := json.Marshal(struct {
			IDs  []string
			Rows []pm.TravelPreference
		}{ids, rows})
		sum := sha256.Sum256(raw)
		id = "preference-analysis:" + hex.EncodeToString(sum[:])
	}
	return a.memory.SaveAnalysis(ctx, ref, id, updates, pm.TravelPreferenceAnalysis{InitialSummaryDone: true, CoveredTripIDs: ids, Trigger: trigger}, sources)
}
func stripJSONFence(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	return strings.TrimSpace(text)
}
