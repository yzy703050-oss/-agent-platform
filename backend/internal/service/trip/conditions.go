package trip

import (
	"bytes"
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
)

// MoneyFen never passes JSON amounts through float64. Unknown values remain
// unknown at their caller; a present amount must be finite, exact and in range.
func MoneyFen(raw json.RawMessage) (int64, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return 0, invalid("invalid monetary JSON")
	}
	n, ok := value.(json.Number)
	if !ok {
		return 0, invalid("money must be a nonnegative JSON number")
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || r.Sign() < 0 {
		return 0, invalid("invalid monetary amount")
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, invalid("monetary amount is not representable in integer fen")
	}
	return r.Num().Int64(), nil
}

var sourceConditionKeys = []string{"start_date", "end_date", "departure_date", "arrival_date", "arrival_before", "check_in", "check_out", "nights", "passengers", "guests", "hotel_keywords", "flexible_dates", "hotel_quote_required", "constraints"}
var sourceFilterKeys = []string{"hotel_max_total_cny", "hotel_max_nightly_cny", "train_max_total_cny", "seat_class", "train_kind", "departure_time_after", "departure_time_before", "available_only", "total_budget_cny"}

func rawValue(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func textValue(c model.TripConditions, key string) string {
	var v string
	_ = json.Unmarshal(c[key], &v)
	return v
}
func intValue(c model.TripConditions, key string, def int) int {
	var v int
	if json.Unmarshal(c[key], &v) != nil || v < 1 {
		return def
	}
	return v
}
func boolValue(c model.TripConditions, key string) bool {
	var v bool
	_ = json.Unmarshal(c[key], &v)
	return v
}
func filterValues(c model.TripConditions) model.TripConditions {
	var out model.TripConditions
	_ = json.Unmarshal(c["constraints"], &out)
	return out
}
func missingValue(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func ValidateConditions(c model.TripConditions) error {
	for key, raw := range c {
		if !slices.Contains(sourceConditionKeys, key) {
			return invalid("unsupported condition: " + key)
		}
		if missingValue(raw) {
			continue
		}
		switch key {
		case "constraints":
			var values model.TripConditions
			if json.Unmarshal(raw, &values) != nil {
				return invalid("constraints must be an object")
			}
			if err := validateSourceFilters(values); err != nil {
				return err
			}
		case "nights", "passengers", "guests":
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 1 {
				return invalid(key + " must be a positive integer")
			}
		case "flexible_dates", "hotel_quote_required":
			var flag bool
			if json.Unmarshal(raw, &flag) != nil {
				return invalid(key + " must be boolean")
			}
		case "arrival_before":
			if _, err := time.Parse(time.RFC3339, textValue(c, key)); err != nil {
				return invalid("arrival_before requires ISO date/time and timezone")
			}
		case "hotel_keywords":
			if strings.TrimSpace(textValue(c, key)) == "" {
				return invalid("hotel_keywords must be nonempty text")
			}
		default:
			if _, err := parseDate(textValue(c, key)); err != nil {
				return invalid(key + " requires YYYY-MM-DD")
			}
		}
	}
	return nil
}

func validateSourceFilters(values model.TripConditions) error {
	for key, raw := range values {
		if !slices.Contains(sourceFilterKeys, key) {
			return invalid("unsupported constraint: " + key)
		}
		if strings.HasSuffix(key, "_cny") {
			if _, err := MoneyFen(raw); err != nil {
				return err
			}
			continue
		}
		switch key {
		case "available_only":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return invalid("available_only must be boolean")
			}
		case "train_kind":
			if !slices.Contains([]string{"high_speed", "emu", "other"}, textValue(values, key)) {
				return invalid("unsupported canonical train_kind")
			}
		case "departure_time_after", "departure_time_before":
			if _, err := time.Parse("15:04", textValue(values, key)); err != nil {
				return invalid(key + " requires HH:MM")
			}
		case "seat_class":
			if textValue(values, key) == "" {
				return invalid("seat_class must be nonempty text")
			}
		}
	}
	return nil
}

func mergeConditions(base, patch model.TripConditions) model.TripConditions {
	out := model.TripConditions{}
	for key, value := range base {
		out[key] = slices.Clone(value)
	}
	for key, value := range patch {
		out[key] = slices.Clone(value)
	}
	return out
}
func effectiveConditions(global, local model.TripConditions) model.TripConditions {
	out := model.TripConditions{}
	for _, part := range []model.TripConditions{global, local} {
		for key, value := range part {
			if missingValue(value) {
				continue
			}
			if key == "constraints" {
				var next model.TripConditions
				_ = json.Unmarshal(value, &next)
				out[key] = rawValue(mergeConditions(filterValues(out), next))
			} else {
				out[key] = slices.Clone(value)
			}
		}
	}
	if missingValue(out["passengers"]) {
		out["passengers"] = rawValue(1)
	}
	if missingValue(out["guests"]) {
		out["guests"] = slices.Clone(out["passengers"])
	}
	return out
}
func sourceHard(global model.TripPlan, t model.TripTask, key string) bool {
	if raw := conditionLeaf(global.ConfirmedConditions, key); !missingValue(raw) && global.ConditionStrengths[key] != model.Soft {
		return true
	}
	if key == "departure_date" && len(global.Tasks) > 0 && t.ID == global.Tasks[0].ID && !missingValue(global.ConfirmedConditions["start_date"]) && global.ConditionStrengths["start_date"] != model.Soft {
		return true
	}
	if level, ok := t.ConditionStrengths[key]; ok {
		return level == model.Hard
	}
	if !missingValue(conditionLeaf(t.Conditions, key)) {
		return t.FieldSources[key] == "user" || t.FieldSources[key] == "context" || !missingValue(filterValues(t.Conditions)[key])
	}
	return false
}
func conditionLeaf(c model.TripConditions, key string) json.RawMessage {
	if raw, ok := c[key]; ok {
		return raw
	}
	return filterValues(c)[key]
}

func sourceConstraints(values model.TripConditions, levels map[string]model.ConstraintStrength) (model.TripConstraints, error) {
	if err := validateSourceFilters(values); err != nil {
		return model.TripConstraints{}, err
	}
	out := model.TripConstraints{Strengths: map[string]model.ConstraintStrength{}}
	for key, raw := range values {
		soft := levels[key] == model.Soft
		if strings.HasSuffix(key, "_cny") {
			fen, err := MoneyFen(raw)
			if err != nil {
				return out, err
			}
			switch key {
			case "total_budget_cny":
				out.MaxBudgetFen = &fen
				out.BudgetStrength = model.Hard
				if soft {
					out.BudgetStrength = model.Soft
				}
			case "train_max_total_cny":
				if !soft {
					out.MaxTrainFen = &fen
				}
			case "hotel_max_total_cny":
				if !soft {
					out.MaxHotelTotalFen = &fen
				}
			case "hotel_max_nightly_cny":
				if !soft {
					out.MaxHotelNightFen = &fen
				}
			}
			continue
		}
		if soft {
			continue
		}
		switch key {
		case "train_kind":
			out.TrainKinds = []string{textValue(values, key)}
		case "seat_class":
			out.SeatClasses = []string{textValue(values, key)}
		case "departure_time_after":
			out.DepartureTimeAfter = textValue(values, key)
		case "departure_time_before":
			out.DepartureTimeBefore = textValue(values, key)
		case "available_only":
			v := boolValue(values, key)
			out.AvailableOnly = &v
		}
	}
	return out, nil
}

func projectSourceConditions(p *model.TripPlan, t *model.TripTask) error {
	if err := ValidateConditions(t.Conditions); err != nil {
		return err
	}
	c := effectiveConditions(p.ConfirmedConditions, t.Conditions)
	t.DepartureDate = textValue(c, "departure_date")
	t.ArrivalDate = textValue(c, "arrival_date")
	t.ArrivalBefore = textValue(c, "arrival_before")
	t.CheckIn = textValue(c, "check_in")
	t.CheckOut = textValue(c, "check_out")
	t.Nights = intValue(c, "nights", 0)
	t.Passengers = intValue(c, "passengers", 1)
	t.Guests = intValue(c, "guests", t.Passengers)
	t.HotelKeywords = textValue(c, "hotel_keywords")
	t.HotelQuoteRequired = boolValue(c, "hotel_quote_required")
	if t.CheckIn == "" && t.Train != nil && t.Train.Candidate.ArrivalAt != nil {
		t.CheckIn = t.Train.Candidate.ArrivalAt.In(beijing).Format("2006-01-02")
	}
	if t.CheckIn != "" && t.CheckOut == "" && t.Nights > 0 {
		day, _ := parseDate(t.CheckIn)
		t.CheckOut = day.AddDate(0, 0, t.Nights).Format("2006-01-02")
	}
	if t.CheckIn != "" && t.CheckOut != "" {
		a, _ := parseDate(t.CheckIn)
		b, _ := parseDate(t.CheckOut)
		if t.Nights == 0 {
			t.Nights = int(b.Sub(a) / (24 * time.Hour))
		}
	}
	levels := map[string]model.ConstraintStrength{}
	for key, v := range p.ConditionStrengths {
		levels[key] = v
	}
	for key, v := range t.ConditionStrengths {
		levels[key] = v
	}
	var err error
	t.Constraints, err = sourceConstraints(filterValues(c), levels)
	t.Constraints.MaxBudgetFen = nil
	return err
}
