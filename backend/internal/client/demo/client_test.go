package demo_test

import (
	"context"
	"testing"
	"time"

	"agent-platform/backend/internal/client/demo"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/service/trip"
)

func TestDemoUnknownAvailabilityRetainsCandidates(t *testing.T) {
	only := true
	q := model.TravelQuery{Domain: "train", Origin: "上海", Destination: "杭州", Date: "2026-10-26", Constraints: model.TripConstraints{AvailableOnly: &only}}
	e, err := demo.New(time.Now).Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	e.Query = q
	view := trip.CandidateView(e)
	if !e.IsDemo || view.Status != "partial" || len(view.Candidates) != 2 || len(view.Unknown) != 2 {
		t.Fatalf("unknown demo inventory must remain explicitly unverified, never falsely sold out: status=%s candidates=%d unknown=%d", view.Status, len(view.Candidates), len(view.Unknown))
	}
	for _, c := range view.Candidates {
		if c.Availability != "unknown" || !c.IsDemo {
			t.Fatal("mock stock must use canonical unknown with demo provenance")
		}
	}
	e, err = demo.New(time.Now).Search(context.Background(), model.TravelQuery{Domain: "hotel", City: "杭州"})
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsDemo || len(e.Candidates) != 1 || e.Candidates[0].PriceFen != nil || e.Candidates[0].Availability != "unknown" {
		t.Fatal("demo hotel location fabricated price/stock")
	}
}
