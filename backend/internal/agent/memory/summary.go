package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"agent-platform/backend/internal/model"
)

type SummaryStore interface {
	TravelSummaryReader
	SaveTravelSummary(context.Context, model.TravelExecution, model.TravelSummary) (model.TravelSummary, error)
}
type PreferenceWriter interface {
	Apply(context.Context, model.TravelExecution, string, model.TravelPreferenceCommand) (model.TravelPreference, error)
}
type Maintenance struct {
	store       SummaryStore
	preferences PreferenceWriter
}

func NewMaintenance(s SummaryStore, p PreferenceWriter) *Maintenance {
	return &Maintenance{store: s, preferences: p}
}

// The caller supplies only source-authorized history. Current/unfinished run
// input and tool text are never used for preference inference.
func (m *Maintenance) Maintain(ctx context.Context, ref model.TravelExecution, history []model.Message) error {
	old, err := m.store.ListTravelSummaries(ctx, ref.Scope, ref.ConversationID, ref.RunID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	version := int64(1)
	for _, s := range old {
		if s.Version >= version {
			version = s.Version + 1
		}
		for _, id := range s.SourceRuns {
			known[id] = true
		}
	}
	runs := map[string]bool{}
	for _, msg := range history {
		if msg.RunID != "" && msg.RunID != ref.RunID {
			runs[msg.RunID] = true
		}
	}
	newCount := 0
	for id := range runs {
		if !known[id] {
			newCount++
		}
	}
	if newCount < 5 {
		return nil
	}
	sources := make([]string, 0, len(runs))
	for id := range runs {
		sources = append(sources, id)
	}
	sort.Strings(sources)
	data, _ := json.Marshal(sources)
	digest := sha256.Sum256(data)
	basis := hex.EncodeToString(digest[:])
	patterns := map[string]struct{ Key, Value string }{
		"我习惯坐二等座":   {"seat_class", "二等座"},
		"我更喜欢靠窗座位":  {"seat_position", "靠窗"},
		"我出差优先选择高铁": {"train_kind", "高铁"},
	}
	counts := map[string]map[string]bool{}
	type fact struct {
		PlanID   string   `json:"plan_id"`
		Revision int64    `json:"revision"`
		Routes   []string `json:"routes"`
	}
	var facts []fact
	seenFacts := map[string]bool{}
	for _, msg := range history {
		if !runs[msg.RunID] {
			continue
		}
		if msg.Role == "user" {
			text := strings.TrimRight(strings.TrimSpace(msg.Content), "。！.")
			if _, ok := patterns[text]; ok {
				if counts[text] == nil {
					counts[text] = map[string]bool{}
				}
				counts[text][msg.RunID] = true
			}
		}
		if msg.Role != "tool" {
			continue
		}
		var observation struct {
			Trip model.TripResult `json:"trip"`
		}
		if json.Unmarshal([]byte(msg.Content), &observation) != nil {
			continue
		}
		p := observation.Trip.Plan
		receipt := observation.Trip.Receipt
		if receipt == nil || receipt.PlanID != p.ID || receipt.Revision != p.Revision || p.ID == "" || len(p.Tasks) > 20 {
			continue
		}
		key := p.ID + ":" + msg.ID
		if seenFacts[key] || len(facts) >= 10 {
			continue
		}
		seenFacts[key] = true
		f := fact{PlanID: p.ID, Revision: p.Revision}
		for _, task := range p.Tasks {
			route := task.Origin + "→" + task.Destination + " " + task.DepartureDate
			if len(route) <= 250 {
				f.Routes = append(f.Routes, route)
			}
		}
		facts = append(facts, f)
	}
	for text, runSet := range counts {
		if len(runSet) < 3 {
			continue
		}
		p := patterns[text]
		_, err = m.preferences.Apply(ctx, ref, "memory-analysis:"+basis+":"+p.Key, model.TravelPreferenceCommand{Operation: "set", Key: p.Key, Value: p.Value, Origin: "inferred"})
		if err != nil && !errors.Is(err, model.ErrConflict) {
			return err
		}
	}
	content, err := json.Marshal(struct {
		Basis string `json:"analysis_basis"`
		Facts []fact `json:"saved_plan_facts"`
		Note  string `json:"note"`
	}{basis, facts, "历史规划记录；不代表当前有效票价、库存、政策合规或预订。"})
	if err != nil {
		return err
	}
	// A full, structured record is either stored or omitted; never cut JSON.
	if len(content) > 16000 {
		return model.ErrInvalidInput
	}
	_, err = m.store.SaveTravelSummary(ctx, ref, model.TravelSummary{SourceRuns: sources, Version: version, Content: string(content)})
	return err
}
