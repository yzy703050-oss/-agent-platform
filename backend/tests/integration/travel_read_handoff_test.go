package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	travelagent "agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	pm "agent-platform/backend/internal/model"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type readHandoffModel struct {
	plan   pm.TripPlan
	calls  int
	lookup bool
}

func (m *readHandoffModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	return m, nil
}
func (m *readHandoffModel) Generate(_ context.Context, in []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	m.calls++
	if m.lookup {
		if m.calls == 1 {
			raw, _ := json.Marshal(map[string]any{"request_kind": "lookup", "resource": "trip", "trip_plan_id": m.plan.ID})
			return toolDecision("read-existing", "read", string(raw)), nil
		}
		if m.calls != 2 || !strings.HasPrefix(in[0].Content, "[travel-prompt:role:summary]") {
			return nil, fmt.Errorf("lookup read was promoted into planning")
		}
		return schema.AssistantMessage("已读取行程。", nil), nil
	}
	if m.calls != 1 || !strings.HasPrefix(in[0].Content, "[travel-prompt:role:main]") {
		return nil, fmt.Errorf("explicit existing edit did not run once in Main")
	}
	var contextData struct {
		Tripplan struct {
			ID       string `json:"trip_plan_id"`
			Revision int64  `json:"revision"`
		} `json:"tripplan"`
	}
	_ = json.Unmarshal([]byte(in[len(in)-1].Content), &contextData)
	if contextData.Tripplan.ID != m.plan.ID || contextData.Tripplan.Revision != m.plan.Revision {
		return nil, fmt.Errorf("explicit user trip ID did not hydrate an authorized snapshot before first Main")
	}
	activity := m.plan.Tasks[0].Activities[0]
	activity.Time = "16:00"
	activity.Description = "客户会议"
	raw, _ := json.Marshal(map[string]any{"request_kind": "planning", "tripplan": map[string]any{"operation": "amend", "trip_plan_id": m.plan.ID, "expected_revision": m.plan.Revision, "tasks": []any{map[string]any{"task_id": m.plan.Tasks[0].ID, "components": []string{"content"}, "content": []pm.TripActivity{activity}}}}, "reply": map[string]any{}})
	return toolDecision("edit-existing", "write_trip", string(raw)), nil
}
func (m *readHandoffModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{out}), err
}

func TestTravelProvidedTripContext(t *testing.T) {
	for _, lookup := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit_existing", true: "lookup_does_not_adopt"}[lookup], func(t *testing.T) {
			ctx := context.Background()
			f, ref, _ := travelFixture(t)
			provider := newPerfProvider(time.Now)
			service := tripsvc.NewWithQueries(f.repo, provider, time.Now)
			command := travelCommand()
			command.Tasks[0].Activities = []pm.TripActivity{{ID: "meeting", Purpose: "客户会议", Date: command.Tasks[0].DepartureDate, Time: "14:00", Source: "user", Strength: pm.Hard}}
			saved, err := service.Apply(ctx, ref, "seed-read-edit", command)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := service.Query(ctx, ref, saved.Queries[0])
			if err != nil {
				t.Fatal(err)
			}
			saved, err = service.Apply(ctx, ref, "seed-read-select", pm.TripCommand{Operation: "select", PlanID: saved.Plan.ID, ExpectedRevision: saved.Plan.Revision, Selections: []pm.TripSelection{{TaskID: saved.Plan.Tasks[0].ID, Domain: "train", Ref: pm.TravelCandidateRef{QueryID: evidence.ID, ResultRevision: evidence.ResultRevision, CandidateID: evidence.Candidates[0].ID}}}})
			if err != nil {
				t.Fatal(err)
			}
			seedTrainRef := saved.Plan.Tasks[0].Train.Ref
			seedQueries := provider.Calls["train"]
			// The behavior under test is history discovery, not initial tool-message hydration.
			model := &readHandoffModel{plan: saved.Plan, lookup: lookup}
			agent, err := travelagent.NewAgent(travelagent.Config{Model: model, Trips: service, Preferences: prefsvc.New(f.repo), Decisions: f.repo, Checkpoints: f.repo, Identity: "read-handoff"})
			if err != nil {
				t.Fatal(err)
			}
			run, err := f.repo.GetRun(ctx, f.scope, ref.RunID)
			if err != nil {
				t.Fatal(err)
			}
			query := fmt.Sprintf("把行程%s客户会议改为16:00，只改活动，其他保持。", saved.Plan.ID)
			if lookup {
				query = "只查看行程" + saved.Plan.ID
			}
			execution, err := agent.NewExecution(ctx, contracts.RunInput{Scope: f.scope, Run: run, Messages: []pm.Message{{RunID: ref.RunID, Role: "user", Content: query}}, Limits: contracts.Limits{Iterations: 4, ToolTimeout: time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = execution.Start(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			current, err := service.Read(ctx, f.scope, saved.Plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "16:00"
			if lookup {
				want = "14:00"
			}
			wantCalls := 1
			if lookup {
				wantCalls = 2
			}
			if model.calls != wantCalls || current.Tasks[0].Activities[0].Time != want {
				t.Fatal("read handoff lost update or expanded lookup scope", model.calls, current.Tasks[0].Activities)
			}
			if current.Tasks[0].Train == nil || current.Tasks[0].Train.Ref != seedTrainRef || provider.Calls["train"] != seedQueries {
				t.Fatal("local edit changed train evidence or queried suppliers")
			}
		})
	}
}
