package trip

import (
	"context"
	"sync"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

type sourceQueryStore struct {
	Store
	mu   sync.Mutex
	rows map[string]model.TravelEvidence
}

func (s *sourceQueryStore) FindTravelQuery(_ context.Context, _ model.TravelExecution, key string) (model.TravelEvidence, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rows[key]
	return v, ok, nil
}
func (s *sourceQueryStore) SaveTravelQuery(_ context.Context, _ model.TravelExecution, key string, e model.TravelEvidence) (model.TravelEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[key] = e
	return e, nil
}

type sourceQueryClient func(context.Context, model.TravelQuery) (model.TravelEvidence, error)

func (f sourceQueryClient) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	return f(ctx, q)
}

func TestTravelSourceQueries(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	ref := model.TravelExecution{Scope: model.Scope{UserID: "owner", SessionID: "session"}, ConversationID: "conv", RunID: "run", Attempt: 1}
	stamp := func(day, clock string) *time.Time {
		v, err := time.Parse(time.RFC3339, day+"T"+clock+":00+08:00")
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	t.Run("equivalent raw cache separate ranked views and refresh", func(t *testing.T) {
		calls := 0
		store := &sourceQueryStore{rows: map[string]model.TravelEvidence{}}
		svc := NewWithQueries(store, sourceQueryClient(func(_ context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
			calls++
			if len(q.Constraints.TrainKinds) > 0 || len(q.Ranking) > 0 {
				t.Fatal("projection leaked into physical query")
			}
			return model.TravelEvidence{Status: "ok", Source: model.TravelSource{Provider: "fixture", FetchedAt: now}, Candidates: []model.TravelCandidate{
				{ID: "early", Name: "G1", TrainKind: "high_speed", DepartureAt: stamp("2026-10-26", "08:00"), ArrivalAt: stamp("2026-10-26", "09:00")},
				{ID: "late", Name: "G2", TrainKind: "high_speed", DepartureAt: stamp("2026-10-26", "18:00"), ArrivalAt: stamp("2026-10-26", "19:00")},
			}}, nil
		}), func() time.Time { return now })
		q := model.TravelQuery{SourceVersion: 1, Domain: "train", Origin: "上海", Destination: "杭州", Date: "2026-10-26", Ranking: []model.TripRanking{{Field: "departure_at", Direction: "desc"}}}
		first, err := svc.Query(context.Background(), ref, q)
		if err != nil {
			t.Fatal(err)
		}
		view := CandidateView(first)
		if len(view.Candidates) != 2 || view.Candidates[0].ID != "late" {
			t.Fatalf("late first ranking lost: %+v", view)
		}
		if first.ExpiresAt.Sub(now) != time.Hour {
			t.Fatal("source TTL must be one hour")
		}
		q.Ranking[0].Direction = "asc"
		q.Constraints.TrainKinds = []string{"high_speed"}
		second, err := svc.Query(context.Background(), ref, q)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || CandidateView(second).Candidates[0].ID != "early" {
			t.Fatal("rank/constraints caused refetch or stale projection", calls)
		}
		q.Refresh = true
		if _, err = svc.Query(context.Background(), ref, q); err != nil || calls != 2 {
			t.Fatal("explicit refresh was ignored", calls, err)
		}
	})
	t.Run("arrival date search is bounded and stops on known eligible evidence", func(t *testing.T) {
		days := []string{}
		svc := NewWithQueries(&sourceQueryStore{rows: map[string]model.TravelEvidence{}}, sourceQueryClient(func(_ context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
			days = append(days, q.Date)
			arrival := "2026-10-27"
			if q.Date == "2026-10-25" {
				arrival = "2026-10-26"
			}
			return model.TravelEvidence{Status: "ok", Source: model.TravelSource{Provider: "fixture", FetchedAt: now}, Candidates: []model.TravelCandidate{{ID: "night", Name: "G3", TrainKind: "high_speed", DepartureAt: stamp(q.Date, "23:00"), ArrivalAt: stamp(arrival, "07:00")}}}, nil
		}), func() time.Time { return now })
		e, err := svc.Query(context.Background(), ref, model.TravelQuery{SourceVersion: 1, Domain: "train", Origin: "上海", Destination: "杭州", ArrivalDate: "2026-10-26"})
		if err != nil || len(days) != 2 || days[0] != "2026-10-26" || days[1] != "2026-10-25" || len(e.Candidates) != 1 {
			t.Fatal(days, e, err)
		}
	})
	t.Run("unknown dates cannot be fixed by blindly changing departure dates", func(t *testing.T) {
		calls := 0
		svc := NewWithQueries(&sourceQueryStore{rows: map[string]model.TravelEvidence{}}, sourceQueryClient(func(_ context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
			calls++
			return model.TravelEvidence{Status: "ok", Source: model.TravelSource{Provider: "fixture", FetchedAt: now}, Candidates: []model.TravelCandidate{{ID: "clockonly", Name: "G4"}}}, nil
		}), func() time.Time { return now })
		e, err := svc.Query(context.Background(), ref, model.TravelQuery{SourceVersion: 1, Domain: "train", Origin: "上海", Destination: "杭州", ArrivalDate: "2026-10-26"})
		if err != nil || calls != 1 || e.Status != "partial" || len(e.Candidates) != 0 {
			t.Fatal(calls, e, err)
		}
	})
}
