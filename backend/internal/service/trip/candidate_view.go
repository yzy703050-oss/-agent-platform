package trip

import (
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
)

type TravelCandidateView struct {
	Candidates []model.TravelCandidate
	Unknown    map[string][]string
	Status     string
	Total      int
	Excluded   int
	HasMore    bool
}

// CandidateView projects raw evidence for one caller. Unknown hard evidence is
// retained behind verified candidates; it is never converted into a zero price.
func CandidateView(e model.TravelEvidence) TravelCandidateView {
	v := TravelCandidateView{Status: e.Status, Unknown: map[string][]string{}}
	candidates := []model.TravelCandidate{}
	for _, c := range e.Candidates {
		gaps, eligible := candidateEligibility(c, e.Query)
		if eligible {
			candidates = append(candidates, c)
			if len(gaps) > 0 {
				v.Unknown[c.ID] = gaps
			}
		}
	}
	for i := len(e.Query.Ranking) - 1; i >= 0; i-- {
		rule := e.Query.Ranking[i]
		slices.SortStableFunc(candidates, func(a, b model.TravelCandidate) int {
			av, aok := rankValue(a, e.Query, rule)
			bv, bok := rankValue(b, e.Query, rule)
			if aok != bok {
				if aok {
					return -1
				}
				return 1
			}
			if !aok {
				return 0
			}
			comparison := compareRank(av, bv)
			if rule.Direction == "desc" {
				return -comparison
			}
			return comparison
		})
	}
	slices.SortStableFunc(candidates, func(a, b model.TravelCandidate) int {
		ak, bk := len(v.Unknown[a.ID]) > 0, len(v.Unknown[b.ID]) > 0
		if ak == bk {
			return 0
		}
		if ak {
			return 1
		}
		return -1
	})
	v.Total = len(candidates)
	v.Excluded = len(e.Candidates) - len(candidates)
	v.HasMore = len(candidates) > 5
	v.Candidates = candidates[:min(5, len(candidates))]
	if len(v.Unknown) > 0 && v.Status == "ok" {
		v.Status = "partial"
	}
	return v
}
func candidateEligibility(c model.TravelCandidate, q model.TravelQuery) ([]string, bool) {
	gaps := []string{}
	eligible := true
	check := func(key string, known, match bool) {
		if !known {
			gaps = append(gaps, key)
		} else if !match {
			eligible = false
		}
	}
	filters := q.Constraints
	if q.Domain == "train" {
		if len(filters.TrainKinds) > 0 {
			kind := canonicalTrainKind(c.TrainKind)
			check("train_kind", kind != "" && kind != "unknown", slices.ContainsFunc(filters.TrainKinds, func(w string) bool { return canonicalTrainKind(w) == kind }))
		}
		if len(filters.SeatClasses) > 0 {
			check("seat_class", c.SeatClass != "", slices.Contains(filters.SeatClasses, c.SeatClass))
		}
		if filters.MaxTrainFen != nil {
			amount := new(big.Int)
			if c.PriceFen != nil {
				amount.Mul(big.NewInt(*c.PriceFen), big.NewInt(int64(max(1, q.Passengers))))
			}
			check("train_max_total_cny", c.PriceFen != nil, amount.Cmp(big.NewInt(*filters.MaxTrainFen)) <= 0)
		}
		if filters.DepartureTimeAfter != "" || filters.DepartureTimeBefore != "" {
			if c.DepartureAt == nil {
				eligible = false
			} else {
				clock := c.DepartureAt.In(beijing).Format("15:04")
				if filters.DepartureTimeAfter != "" && clock < filters.DepartureTimeAfter || filters.DepartureTimeBefore != "" && clock > filters.DepartureTimeBefore {
					eligible = false
				}
			}
		}
		if filters.AvailableOnly != nil && *filters.AvailableOnly {
			check("available_only", c.Availability != "" && c.Availability != "unknown", c.Availability == "available")
		}
		if q.ArrivalDate != "" {
			check("arrival_date", c.ArrivalAt != nil, c.ArrivalAt != nil && c.ArrivalAt.In(beijing).Format("2006-01-02") == q.ArrivalDate)
		}
		if q.ArrivalBefore != "" {
			bound, err := time.Parse(time.RFC3339, q.ArrivalBefore)
			check("arrival_before", c.ArrivalAt != nil && err == nil, c.ArrivalAt != nil && !c.ArrivalAt.After(bound))
		}
	} else if q.Domain == "hotel" {
		if filters.MaxHotelTotalFen != nil {
			check("hotel_max_total_cny", c.PriceFen != nil, c.PriceFen != nil && *c.PriceFen <= *filters.MaxHotelTotalFen)
		}
		if filters.MaxHotelNightFen != nil {
			in, ie := parseDate(q.CheckIn)
			out, oe := parseDate(q.CheckOut)
			nights := int64(out.Sub(in).Hours() / 24)
			known := c.PriceFen != nil && ie == nil && oe == nil && nights > 0
			match := false
			if known {
				match = big.NewInt(*c.PriceFen).Cmp(new(big.Int).Mul(big.NewInt(*filters.MaxHotelNightFen), big.NewInt(nights))) <= 0
			}
			check("hotel_max_nightly_cny", known, match)
		}
	}
	return gaps, eligible
}
func rankValue(c model.TravelCandidate, q model.TravelQuery, rule model.TripRanking) (any, bool) {
	var value any
	switch rule.Field {
	case "price_cny", "stay_total_cny":
		if c.PriceFen != nil {
			value = new(big.Rat).SetInt64(*c.PriceFen)
		}
	case "nightly_cny":
		in, ie := parseDate(q.CheckIn)
		out, oe := parseDate(q.CheckOut)
		nights := int64(out.Sub(in).Hours() / 24)
		if ie == nil && oe == nil && nights > 0 && c.PriceFen != nil {
			value = new(big.Rat).SetFrac(big.NewInt(*c.PriceFen), big.NewInt(nights))
		}
	case "duration_minutes":
		if c.DepartureAt != nil && c.ArrivalAt != nil {
			value = new(big.Rat).SetInt64(int64(c.ArrivalAt.Sub(*c.DepartureAt)))
		}
	case "departure_at":
		if c.DepartureAt != nil {
			value = new(big.Rat).SetInt64(c.DepartureAt.UnixNano())
		}
	case "arrival_at":
		if c.ArrivalAt != nil {
			value = new(big.Rat).SetInt64(c.ArrivalAt.UnixNano())
		}
	case "train_kind":
		if c.TrainKind != "" && c.TrainKind != "unknown" {
			value = canonicalTrainKind(c.TrainKind)
		}
	case "seat_class":
		if c.SeatClass != "" {
			value = c.SeatClass
		}
	case "hotel_name":
		value = c.Name
	case "address":
		if c.Address != "" {
			value = c.Address
		}
	}
	if len(rule.Preferred) > 0 && string(rule.Preferred) != "null" {
		var preferred any
		_ = json.Unmarshal(rule.Preferred, &preferred)
		list, ok := preferred.([]any)
		if !ok {
			list = []any{preferred}
		}
		for _, p := range list {
			if text, ok := p.(string); ok {
				if actual, ok := value.(string); ok && (actual == text || strings.Contains(actual, text)) {
					return big.NewRat(0, 1), true
				}
			}
		}
		return big.NewRat(1, 1), true
	}
	return value, value != nil
}
func compareRank(a, b any) int {
	if v, ok := a.(*big.Rat); ok {
		return v.Cmp(b.(*big.Rat))
	}
	return strings.Compare(a.(string), b.(string))
}
