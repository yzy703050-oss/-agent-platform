package trip

import (
	"context"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

type inspectScopeStore struct {
	Store
	plan model.TripPlan
}

func (s *inspectScopeStore) GetTrip(context.Context, model.Scope, string) (model.TripPlan, error) {
	return s.plan, nil
}

func TestTravelInspectIgnoresInactiveStayQueries(t *testing.T) {
	store := &inspectScopeStore{plan: model.TripPlan{ID: "p", ConversationID: "c", SourceVersion: 1, Tasks: []model.TripTask{{ID: "t", Origin: "上海", Destination: "南京", DepartureDate: "2026-10-26", RequiresHotel: false, Items: []model.TripQueryItem{{Domain: "hotel"}, {Domain: "train"}}}}}}
	s := New(store, time.Now)
	result, err := s.Inspect(context.Background(), model.TravelExecution{Scope: model.Scope{UserID: "u", SessionID: "s"}, ConversationID: "c"}, "p")
	if err != nil || len(result.Queries) != 1 || result.Queries[0].Domain != "train" {
		t.Fatalf("inactive old hotel query invalidated current plan: %+v %v", result.Queries, err)
	}
	if len(store.plan.Tasks[0].Items) != 2 || store.plan.Tasks[0].Items[0].Domain != "hotel" {
		t.Fatal("inspection mutated historical query map")
	}
}
