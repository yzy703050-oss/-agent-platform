package trip

import (
	"encoding/json"
	"slices"

	"agent-platform/backend/internal/model"
)

// Hydrate saved Go MVP fields before applying any native patch. An empty patch
// must not make a previously absent conditions map erase stored route facts.
func hydrateLegacySource(p *model.TripPlan) error {
	filters, levels, err := legacySourceFilters(p.Constraints)
	if err != nil {
		return err
	}
	base := model.TripConditions{}
	if len(filters) > 0 {
		base["constraints"] = rawValue(filters)
	}
	p.ConfirmedConditions = mergeConditions(base, p.ConfirmedConditions)
	if p.ConditionStrengths == nil {
		p.ConditionStrengths = map[string]model.ConstraintStrength{}
	}
	for key, value := range levels {
		if p.ConditionStrengths[key] == "" {
			p.ConditionStrengths[key] = value
		}
	}
	for i := range p.Tasks {
		t := &p.Tasks[i]
		filters, levels, err := legacySourceFilters(t.Constraints)
		if err != nil {
			return err
		}
		base := legacyConditions(*t)
		if len(filters) > 0 {
			base["constraints"] = rawValue(filters)
		}
		if t.Passengers > 0 {
			base["passengers"] = rawValue(t.Passengers)
		}
		if t.Guests > 0 {
			base["guests"] = rawValue(t.Guests)
		}
		t.Conditions = effectiveConditions(base, t.Conditions)
		if t.ConditionStrengths == nil {
			t.ConditionStrengths = map[string]model.ConstraintStrength{}
		}
		for key, value := range levels {
			if t.ConditionStrengths[key] == "" {
				t.ConditionStrengths[key] = value
			}
		}
		t.Revision = max(1, t.Revision)
	}
	return nil
}
func legacySourceFilters(c model.TripConstraints) (model.TripConditions, map[string]model.ConstraintStrength, error) {
	out := model.TripConditions{}
	levels := map[string]model.ConstraintStrength{}
	for _, field := range []struct {
		key   string
		value *int64
	}{{"total_budget_cny", c.MaxBudgetFen}, {"train_max_total_cny", c.MaxTrainFen}, {"hotel_max_nightly_cny", c.MaxHotelNightFen}, {"hotel_max_total_cny", c.MaxHotelTotalFen}} {
		if field.value != nil {
			out[field.key] = json.RawMessage(moneyText(*field.value))
		}
	}
	if c.BudgetStrength == model.Soft {
		levels["total_budget_cny"] = model.Soft
	}
	if len(c.TrainKinds) > 1 || len(c.SeatClasses) > 1 {
		return nil, nil, invalid("legacy constraint alternatives require explicit native migration; stored facts were retained")
	}
	if len(c.TrainKinds) == 1 {
		out["train_kind"] = rawValue(canonicalTrainKind(c.TrainKinds[0]))
		if c.Strengths["train_kinds"] == model.Soft {
			levels["train_kind"] = model.Soft
		}
	}
	if len(c.SeatClasses) == 1 {
		out["seat_class"] = rawValue(c.SeatClasses[0])
		if c.Strengths["seat_classes"] == model.Soft {
			levels["seat_class"] = model.Soft
		}
	}
	if c.DepartureTimeAfter != "" {
		out["departure_time_after"] = rawValue(c.DepartureTimeAfter)
	}
	if c.DepartureTimeBefore != "" {
		out["departure_time_before"] = rawValue(c.DepartureTimeBefore)
	}
	if c.AvailableOnly != nil {
		out["available_only"] = rawValue(*c.AvailableOnly)
	}
	return out, levels, nil
}

func canonicalLegacyConstraints(c model.TripConstraints) model.TripConstraints {
	c.TrainKinds = slices.Clone(c.TrainKinds)
	for i, value := range c.TrainKinds {
		c.TrainKinds[i] = canonicalTrainKind(value)
	}
	return c
}
