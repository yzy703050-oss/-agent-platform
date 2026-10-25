package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-platform/backend/internal/agent/contracts"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	trip "agent-platform/backend/internal/service/trip"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestTravelEvaluationContract(t *testing.T) {
	t.Run("record_real_input_without_extra_generation", func(t *testing.T) {
		inner := &perfEchoModel{}
		log := &perfInputLog{}
		recording := &perfRecordingModel{inner: inner, log: log}
		tools, err := traveltools.SourceProfile("main")
		if err != nil {
			t.Fatal(err)
		}
		bound, err := recording.WithTools(tools)
		if err != nil {
			t.Fatal(err)
		}
		_, err = bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("安排上海到杭州出差")})
		if err != nil {
			t.Fatal(err)
		}
		if inner.calls != 1 || len(log.rows) != 1 || len(log.rows[0].Messages) != 1 || len(log.rows[0].Tools) != len(tools) {
			t.Fatal("input recording changed calls or dropped tools")
		}
		raw, _ := json.Marshal(log.rows[0].Tools)
		if !strings.Contains(string(raw), "request_kind") || !strings.Contains(string(raw), "tripplan") {
			t.Fatal("actual parameter schema was lost from input recording", string(raw))
		}
	})
	t.Run("ordinary_provider_and_policy_material", func(t *testing.T) {
		p := newPerfProvider(time.Now)
		for _, q := range []pm.TravelQuery{{Domain: "train", Origin: "上海", Destination: "杭州", Date: "2026-11-01"}, {Domain: "hotel", City: "杭州", CheckIn: "2026-11-01", CheckOut: "2026-11-02"}, {Domain: "weather", City: "杭州", Date: "2026-11-01"}} {
			e, err := p.Search(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(e)
			if e.IsDemo || strings.Contains(string(raw), "演示") || strings.Contains(string(raw), "虚构") {
				t.Errorf("provider leaks fixture provenance into model observation: %s", q.Domain)
			}
			if q.Domain == "train" || q.Domain == "hotel" {
				if len(e.Candidates) == 0 || e.Candidates[0].PriceFen == nil || e.Candidates[0].Availability != "available" {
					t.Errorf("normal path lacks price/stock evidence: %s", q.Domain)
				}
			}
		}
		policy, err := (perfPolicy{}).Search(context.Background(), "安排出差")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(policy)
		if policy.IsDemo || strings.Contains(string(raw), "演示") || strings.Contains(string(raw), "虚构") {
			t.Error("policy injects fixture disclaimers instead of ordinary evidence")
		}
	})
	t.Run("hotel_fixture_accepts_real_physical_request", func(t *testing.T) {
		q := trip.PhysicalQuery(pm.TravelQuery{SourceVersion: 1, Domain: "hotel", City: "杭州", CheckIn: "2026-11-01", CheckOut: "2026-11-02", Guests: 1})
		e, err := newPerfProvider(time.Now).Search(context.Background(), q)
		if err != nil || e.Status != "ok" || len(e.Candidates) != 1 || e.Candidates[0].PriceFen == nil || *e.Candidates[0].PriceFen != 30000 {
			t.Fatalf("one-night benchmark quote must survive production physical-query projection: %+v, %v", e, err)
		}
	})
	t.Run("business_intent_and_duplicate_selection_are_not_quantity_success", func(t *testing.T) {
		s := validPerfSample()
		evaluatePerfSample(&s)
		if !s.Success {
			t.Fatal("valid positive control was rejected")
		}
		s = validPerfSample()
		s.Plans[0].Tasks[0].Purpose = "visit"
		evaluatePerfSample(&s)
		if s.Success {
			t.Error("business changed to visit passed quantity-only acceptance")
		}
		s = validPerfSample()
		args := `{"request_kind":"planning","tripplan":{"operation":"select","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t1","selection_refs":{"train":{"query_id":"q1","result_revision":1,"candidate_id":"c1"}}}]}}`
		for _, id := range []string{"a", "b", "c"} {
			if id == "b" {
				// An intervening amend is not proof of new selection evidence.
				amend, _ := json.Marshal(pm.Message{Role: "assistant", ToolCalls: []pm.ToolCall{{ID: "amend", Name: "write_trip", Arguments: `{"tripplan":{"operation":"amend","trip_plan_id":"p"}}`}}})
				result, _ := json.Marshal(pm.TravelToolResult{CallID: "amend", Name: "write_trip", Status: "partial", Operation: "amend", TripPlanID: "p", Revision: 4})
				obs, _ := json.Marshal(pm.Message{Role: "tool", ToolCallID: "amend", Content: string(result)})
				s.Events = append(s.Events, contracts.Event{Type: "message", Payload: amend}, contracts.Event{Type: "message", Payload: obs})
			}
			msg, _ := json.Marshal(pm.Message{Role: "assistant", ToolCalls: []pm.ToolCall{{ID: id, Name: "write_trip", Arguments: args}}})
			result, _ := json.Marshal(pm.TravelToolResult{CallID: id, Name: "write_trip", Status: "partial", Operation: "select", TripPlanID: "p", Revision: 3})
			obs, _ := json.Marshal(pm.Message{Role: "tool", ToolCallID: id, Content: string(result)})
			s.Events = append(s.Events, contracts.Event{Type: "message", Payload: msg}, contracts.Event{Type: "message", Payload: obs})
		}
		evaluatePerfSample(&s)
		if s.Success {
			t.Error("three identical committed selections passed acceptance")
		}
		if s.DuplicateSelections != 2 {
			t.Errorf("intervening amend hid a repeated selection: duplicates=%d", s.DuplicateSelections)
		}
	})
	t.Run("activity_edit_keeps_exact_train_ref", func(t *testing.T) {
		s := validPerfSample()
		s.Case = "modify_activity"
		s.Plans[0].Tasks = s.Plans[0].Tasks[:1]
		s.Plans[0].Tasks[0].Activities[0].Time = "16:00"
		ref := s.Plans[0].Tasks[0].Train.Ref
		s.SeedTrainRef = &ref
		s.SeedRevisions = 2
		evaluatePerfSample(&s)
		if !s.Success {
			t.Fatal("positive edit rejected", s.FailureReasons)
		}
		s.Plans[0].Tasks[0].Train.Ref.CandidateID = "different"
		evaluatePerfSample(&s)
		if s.Success {
			t.Fatal("changed train identity passed edit acceptance")
		}
	})
}

type perfEchoModel struct{ calls int }

func (m *perfEchoModel) WithTools([]*schema.ToolInfo) (em.ToolCallingChatModel, error) { return m, nil }
func (m *perfEchoModel) Generate(context.Context, []*schema.Message, ...em.Option) (*schema.Message, error) {
	m.calls++
	return schema.AssistantMessage("已保存", nil), nil
}
func (m *perfEchoModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, err := m.Generate(ctx, in, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{v}), err
}

func validPerfSample() perfSample {
	tasks := []pm.TripTask{}
	cities := []string{"上海", "杭州", "南京", "上海"}
	for i := 0; i < 3; i++ {
		ref := pm.TravelCandidateRef{QueryID: "q" + string(rune('1'+i)), ResultRevision: 1, CandidateID: "c" + string(rune('1'+i))}
		task := pm.TripTask{ID: "t" + string(rune('1'+i)), Purpose: "business", Origin: cities[i], Destination: cities[i+1], DepartureDate: time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Train: &pm.TravelSelection{Ref: ref}}
		if i < 2 {
			task.RequiresHotel = true
			task.CheckIn = task.DepartureDate
			task.CheckOut = time.Date(2026, 11, 2+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
			task.Hotel = &pm.TravelSelection{Ref: ref}
			task.Activities = []pm.TripActivity{{Date: task.DepartureDate, Time: "14:00", Purpose: []string{"客户会议", "项目交流"}[i]}}
		} else {
			task.Purpose = "return"
		}
		tasks = append(tasks, task)
	}
	return perfSample{Case: "multicity", Plans: []pm.TripPlan{{ID: "p", Tasks: tasks}}, Revisions: 3}
}
