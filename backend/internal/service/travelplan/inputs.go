package travelplan

import (
	"encoding/json"
	"strings"

	v1 "agent-platform/backend/internal/model/runtimev1"
)

// QueryInput expands only submitted parameters and explicitly associated route
// context. A missing required value stays blocked; there are no guessed dates,
// passenger counts, origin cities or fallback searches.
func QueryInput(definition v1.PlanDefinition, task v1.TaskDefinition, item v1.QueryItem) (map[string]json.RawMessage, string, error) {
	parameters, err := json.Marshal(task.Parameters)
	if err != nil {
		return nil, "", err
	}
	var domains map[string]json.RawMessage
	if err = json.Unmarshal(parameters, &domains); err != nil {
		return nil, "", err
	}
	input := map[string]json.RawMessage{}
	if err = json.Unmarshal(domains[item.Domain], &input); err != nil {
		return nil, "", err
	}
	put := func(key string, value string) {
		if _, present := input[key]; !present && value != "" {
			raw, _ := json.Marshal(value)
			input[key] = raw
		}
	}
	stop := func(id string) v1.Stop {
		for _, s := range definition.Stops {
			if s.StopID == id {
				return s
			}
		}
		return v1.Stop{}
	}
	if task.Target.Kind == "stop" {
		s := stop(task.Target.ID.Value)
		if item.Domain == "hotel" || item.Domain == "weather" {
			put("city", s.City)
		}
		if item.Domain == "hotel" {
			if s.ArrivalDate.Present && !s.ArrivalDate.Null {
				put("check_in", s.ArrivalDate.Value)
			}
			if s.DepartureDate.Present && !s.DepartureDate.Null {
				put("check_out", s.DepartureDate.Value)
			}
		}
	}
	if task.Target.Kind == "leg" && item.Domain == "train" {
		for _, leg := range definition.Legs {
			if leg.LegID != task.Target.ID.Value {
				continue
			}
			put("origin", stop(leg.FromStopID).City)
			put("destination", stop(leg.ToStopID).City)
			if leg.DepartureDate.Present && !leg.DepartureDate.Null {
				put("departure_date", leg.DepartureDate.Value)
			}
		}
	}
	required := map[string][]string{"hotel": {"city", "check_in", "check_out", "guests"}, "train": {"origin", "destination", "departure_date", "passengers"}, "weather": {"city", "date"}, "web": {"query"}}[item.Domain]
	for _, field := range required {
		raw, ok := input[field]
		if !ok || string(raw) == "null" {
			return input, "missing " + item.Domain + "." + field, nil
		}
		var text string
		if json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) == "" {
			return input, "missing " + item.Domain + "." + field, nil
		}
	}
	if len(item.DependsOn) > 0 {
		return input, "query dependencies are unresolved", nil
	}
	return input, "", nil
}

// ArgumentsMatch accounts for the retained provider DTO's representation only.
// It does not ignore differing dates, passenger counts or unknown query fields.
func ArgumentsMatch(domain string, input map[string]json.RawMessage, arguments json.RawMessage) bool {
	raw, err := json.Marshal(input)
	if err != nil {
		return false
	}
	var want, got map[string]json.RawMessage
	if json.Unmarshal(raw, &want) != nil || json.Unmarshal(arguments, &got) != nil || got == nil {
		return false
	}
	if value, present := got["domain"]; present {
		var named string
		if json.Unmarshal(value, &named) != nil || named != "" && named != domain {
			return false
		}
		delete(got, "domain")
	}
	if domain == "train" {
		if date, present := got["date"]; present {
			if _, duplicate := got["departure_date"]; duplicate {
				return false
			}
			got["departure_date"] = date
			delete(got, "date")
		}
	}
	if value, present := want["keywords"]; present && string(value) != "null" && (domain == "hotel" || domain == "train") {
		var words []string
		if json.Unmarshal(value, &words) != nil {
			return false
		}
		if len(words) == 0 {
			delete(want, "keywords")
		}
		if actual, exists := got["keywords"]; exists {
			var text string
			if json.Unmarshal(actual, &text) == nil {
				if text != strings.Join(words, " ") {
					return false
				}
				got["keywords"] = value
			}
		}
	}
	if value, present := got["keywords"]; present && string(value) != "null" && (domain == "hotel" || domain == "train") {
		var words []string
		var text string
		if json.Unmarshal(value, &words) == nil && len(words) == 0 || json.Unmarshal(value, &text) == nil && text == "" {
			delete(got, "keywords")
		}
	}
	a, e1 := canonical(want)
	b, e2 := canonical(got)
	return e1 == nil && e2 == nil && a == b
}
