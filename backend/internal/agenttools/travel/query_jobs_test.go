package travel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"agent-platform/backend/internal/model"
)

type jobTrips struct {
	sourceTripsFixture
	mu       sync.Mutex
	calls    []string
	recorded map[string]bool
}

func (s *jobTrips) Query(_ context.Context, _ model.TravelExecution, q model.TravelQuery) (model.TravelEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.TaskID == "b" && !s.recorded["a"] {
		return model.TravelEvidence{}, errors.New("dependent query preceded persisted observation")
	}
	s.calls = append(s.calls, q.TaskID)
	return model.TravelEvidence{Status: "ok", Query: q}, nil
}
func TestTravelSourceQueryJobs(t *testing.T) {
	result := model.TripResult{Plan: model.TripPlan{ID: "p", Revision: 1}, Receipt: &model.TravelReceipt{OperationID: "op", PlanID: "p", Revision: 1}, Queries: []model.TravelQuery{{PlanID: "p", PlanRevision: 1, TaskID: "a", Domain: "train"}, {PlanID: "p", PlanRevision: 1, TaskID: "b", Domain: "hotel", DependsOn: []string{"a"}}}}
	jobs, err := PlanQueryJobs("op", "call", result)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := PlanQueryJobs("op", "call", result)
	if err != nil || jobs[0].ID != repeat[0].ID || jobs[0].ID == jobs[1].ID {
		t.Fatal("job identity is unstable", err)
	}
	bad := result
	bad.Receipt = &model.TravelReceipt{PlanID: "p", Revision: 1}
	if _, err = PlanQueryJobs("", "", bad); err == nil {
		t.Fatal("unaccepted empty parent identity admitted")
	}
	svc := &jobTrips{recorded: map[string]bool{}}
	r, err := NewRegistry(NativeConfig{Config: Config{Trips: svc}})
	if err != nil {
		t.Fatal(err)
	}
	record := func(_ context.Context, job QueryJob) error {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		svc.recorded[job.Query.TaskID] = true
		return nil
	}
	out, err := r.ExecuteQueryJobs(context.Background(), jobs, record)
	if err != nil || len(svc.calls) != 2 || !svc.recorded["b"] {
		t.Fatal(svc.calls, err)
	}
	// Reconstruct from serialized completed jobs; no supplier request repeats.
	if _, err = r.ExecuteQueryJobs(context.Background(), out, record); err != nil || len(svc.calls) != 2 {
		t.Fatal("completed jobs replayed", svc.calls, err)
	}
	cyc := append([]QueryJob(nil), jobs...)
	cyc[0].Query.DependsOn = []string{"b"}
	if _, err = r.ExecuteQueryJobs(context.Background(), cyc, record); err == nil {
		t.Fatal("cyclic dependency accepted")
	}
}
