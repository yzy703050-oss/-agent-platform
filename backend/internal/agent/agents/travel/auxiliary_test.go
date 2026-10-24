package travel

import (
	pm "agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"os"
	"strings"
	"testing"
)

func TestTravelSourceAuxiliary(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_auxiliary_prompts.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Prompt string `json:"prompt"`
		Input  struct {
			Context, Evidence json.RawMessage
			Previous          string
			Events            json.RawMessage
		} `json:"input"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for name, f := range fixtures {
		t.Run(name, func(t *testing.T) {
			var got string
			switch name {
			case "policy":
				got, err = buildPolicyPrompt(f.Input.Context, f.Input.Evidence)
			case "preference":
				var context struct {
					History         json.RawMessage `json:"history"`
					ExistingSummary string          `json:"existing_summary"`
					Memories        json.RawMessage `json:"memories"`
				}
				if err = json.Unmarshal(f.Input.Context, &context); err != nil {
					t.Fatal(err)
				}
				got, err = buildPreferencePrompt(context.History, context.ExistingSummary, context.Memories)
			case "compaction":
				got, err = buildCompactionPrompt(f.Input.Previous, f.Input.Events)
			default:
				t.Fatal("unknown helper fixture", name)
			}
			if err != nil || got != f.Prompt {
				t.Fatalf("original %s auxiliary prompt differs: err=%v\ngot=%s", name, err, got)
			}
		})
	}
}

type auxiliaryScriptModel struct {
	calls              int
	answer, lastPrompt string
}

func (m *auxiliaryScriptModel) Generate(_ context.Context, messages []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	m.calls++
	m.lastPrompt = messages[0].Content
	return schema.AssistantMessage(m.answer, nil), nil
}
func (m *auxiliaryScriptModel) Stream(ctx context.Context, messages []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, messages, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), err
}

type auxiliaryEvidence struct{ value pm.PolicyEvidence }

func (r *auxiliaryEvidence) Search(context.Context, string) (pm.PolicyEvidence, error) {
	return r.value, nil
}

type analysisFixture struct {
	trips  []pm.TripPlan
	rows   []pm.TravelPreference
	writes int
}

func (f *analysisFixture) ReadAnalysisHistory(context.Context, pm.TravelExecution) ([]pm.TripPlan, error) {
	return f.trips, nil
}
func (f *analysisFixture) ReadEffective(context.Context, pm.Scope, string) ([]pm.TravelPreference, error) {
	return f.rows, nil
}
func (f *analysisFixture) Update(context.Context, pm.TravelExecution, string, pm.TravelMemoryCommand) (pm.TravelMemoryResult, error) {
	return pm.TravelMemoryResult{}, nil
}
func (f *analysisFixture) SaveAnalysis(_ context.Context, _ pm.TravelExecution, _ string, _ []pm.TravelMemoryUpdate, a pm.TravelPreferenceAnalysis, _ []string) (pm.TravelMemoryResult, error) {
	f.writes++
	raw, _ := json.Marshal(a)
	f.rows = []pm.TravelPreference{{Key: "$analysis", Value: string(raw)}}
	return pm.TravelMemoryResult{}, nil
}

func TestTravelSourceAuxiliaryExecution(t *testing.T) {
	t.Run("policy extracts once and retains retrieval metadata", func(t *testing.T) {
		m := &auxiliaryScriptModel{answer: `{"status":"ok","constraints":[],"applicability":{},"references":[],"effective_date":null,"missing_fields":[],"conflicts":[]}`}
		evidence := &auxiliaryEvidence{value: pm.PolicyEvidence{Status: "ok", Fragments: []pm.PolicyFragment{{ID: "real-evidence", Content: "有效政策条款", Source: "制度文档", Metadata: map[string]json.RawMessage{"employee_level": json.RawMessage(`"经理"`), "version": json.RawMessage(`"v3"`)}}}}}
		agent := NewPolicyAgent(m, evidence)
		out, err := agent.Query(context.Background(), pm.TravelExecution{}, pm.TravelPolicyQuery{Context: pm.TripConditions{"original_query": json.RawMessage(`"出差"`)}})
		if err != nil || m.calls != 1 || len(out.References) != 1 || !strings.Contains(m.lastPrompt, "经理") || string(out.References[0].Version) != `"v3"` {
			t.Fatal("policy extraction lost source evidence or added model calls", out, m.calls, err)
		}
		evidence.value.Fragments = nil
		if _, err = agent.Query(context.Background(), pm.TravelExecution{}, pm.TravelPolicyQuery{}); err != nil || m.calls != 1 {
			t.Fatal("no evidence still called model", m.calls, err)
		}
	})
	t.Run("preference trigger uses three new confirmed real completed trips", func(t *testing.T) {
		f := &analysisFixture{}
		for _, id := range []string{"one", "two"} {
			f.trips = append(f.trips, pm.TripPlan{ID: id, Status: "completed", UserConfirmed: true, Validation: pm.TripValidation{Status: "complete"}, SourceRunID: "run-" + id})
		}
		m := &auxiliaryScriptModel{answer: `{"memories":[],"covered_trip_ids":["one","two","three"]}`}
		agent := &PreferenceAgent{model: m, history: f, memory: f}
		if err := agent.Maintain(context.Background(), pm.TravelExecution{}); err != nil || m.calls != 0 {
			t.Fatal("less than three trips triggered analysis", err)
		}
		f.trips = append(f.trips, pm.TripPlan{ID: "three", SourceRunID: "run-three", Status: "completed", UserConfirmed: true, Validation: pm.TripValidation{Status: "complete"}}, pm.TripPlan{ID: "simulated", Status: "completed", UserConfirmed: true, Validation: pm.TripValidation{Status: "complete"}, Tasks: []pm.TripTask{{Train: &pm.TravelSelection{Candidate: pm.TravelCandidate{IsDemo: true}}}}})
		if err := agent.Maintain(context.Background(), pm.TravelExecution{}); err != nil || m.calls != 1 || f.writes != 1 || strings.Contains(m.lastPrompt, "simulated") {
			t.Fatal("wrong analysis samples or marker", m.calls, f.writes, err)
		}
		if err := agent.Maintain(context.Background(), pm.TravelExecution{}); err != nil || m.calls != 1 {
			t.Fatal("covered trips reanalyzed", err)
		}
		m.answer = `{"memories":[],"covered_trip_ids":["invented"]}`
		if _, err := agent.analyze(context.Background(), pm.TravelExecution{}, "user-requested", true); err == nil || f.writes != 1 {
			t.Fatal("invalid coverage committed")
		}
	})
}
