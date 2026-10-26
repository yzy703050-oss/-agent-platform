package trip

import (
	"slices"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
)

func TestTravelSourceStayCoversDeparture(t *testing.T) {
	departure := time.Date(2026, 10, 28, 14, 0, 0, 0, beijing)
	for _, tc := range []struct {
		name, checkout string
		hotel, gap     bool
	}{
		{"missing_last_night", "2026-10-27", true, true},
		{"covered", "2026-10-28", true, false},
		{"no_hotel_requested", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := model.TripPlan{Tasks: []model.TripTask{{ID: "visit", Destination: "杭州", RequiresHotel: tc.hotel, CheckOut: tc.checkout}, {ID: "return", Origin: "杭州", Train: &model.TravelSelection{Candidate: model.TravelCandidate{DepartureAt: &departure}}}}}
			v := model.TripValidation{Status: "complete"}
			if err := validateSourceTimeline(p, &v); err != nil {
				t.Fatal(err)
			}
			gap := slices.ContainsFunc(v.Issues, func(issue model.TripIssue) bool { return issue.Code == "hotel_stay_gap" && issue.Blocking })
			if gap != tc.gap || tc.gap && v.Status == "complete" {
				t.Fatalf("stay coverage=%+v want gap=%v", v, tc.gap)
			}
		})
	}
}

func TestTravelSourceStayGapBeforeSelection(t *testing.T) {
	p := model.TripPlan{Tasks: []model.TripTask{{ID: "visit", Destination: "杭州", RequiresHotel: true, CheckOut: "2026-10-27"}, {ID: "return", Origin: "杭州", DepartureDate: "2026-10-28"}}}
	v := model.TripValidation{Status: "complete"}
	if err := validateSourceTimeline(p, &v); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(v.Issues, func(issue model.TripIssue) bool { return issue.Code == "hotel_stay_gap" && issue.Blocking }) {
		t.Fatal("known stay gap hidden until selection", v)
	}
}

func TestTravelInactiveStayDoesNotConstrainTransit(t *testing.T) {
	departure := time.Date(2026, 10, 26, 14, 0, 0, 0, beijing)
	p := model.TripPlan{Tasks: []model.TripTask{{ID: "transit", RequiresHotel: false, CheckOut: "2026-10-28"}, {ID: "next", Train: &model.TravelSelection{Candidate: model.TravelCandidate{DepartureAt: &departure}}}}}
	v := model.TripValidation{Status: "complete"}
	if err := validateSourceTimeline(p, &v); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(v.Issues, func(issue model.TripIssue) bool { return issue.Code == "departure_before_previous_checkout" }) {
		t.Fatal("inactive stay blocked transit", v)
	}
}
