package trip

import (
	"slices"
	"time"

	"agent-platform/backend/internal/model"
)

// QueryFromItem validates source-domain parameters before external effects.
func QueryFromItem(item model.TripQueryItem) (model.TravelQuery, error) {
	allowed := map[string][]string{
		"train":   {"origin", "destination", "departure_date", "start_date", "arrival_date", "arrival_before", "passengers"},
		"hotel":   {"city", "destination", "check_in", "check_out", "guests", "keywords", "search_kind"},
		"weather": {"city", "date"}, "web": {"query"},
	}
	keys, ok := allowed[item.Domain]
	if !ok {
		return model.TravelQuery{}, invalid("unsupported query domain")
	}
	for key := range item.Parameters {
		if !slices.Contains(keys, key) {
			return model.TravelQuery{}, invalid("unsupported query parameter for " + item.Domain + ": " + key)
		}
	}
	constraints, err := sourceConstraints(item.Constraints, item.ConditionStrengths)
	if err != nil {
		return model.TravelQuery{}, err
	}
	q := model.TravelQuery{SourceVersion: 1, OriginalParameters: mergeConditions(nil, item.Parameters), Domain: item.Domain, Constraints: constraints, Ranking: item.Ranking, DependsOn: item.DependsOn, Refresh: item.Refresh}
	p := item.Parameters
	switch item.Domain {
	case "train":
		q.Origin = textValue(p, "origin")
		q.Destination = textValue(p, "destination")
		q.Date = textValue(p, "departure_date")
		if q.Date == "" {
			q.Date = textValue(p, "start_date")
		}
		q.ArrivalDate = textValue(p, "arrival_date")
		q.ArrivalBefore = textValue(p, "arrival_before")
		q.Passengers = intValue(p, "passengers", 1)
		if !missingValue(p["passengers"]) && intValue(p, "passengers", 0) == 0 {
			return q, invalid("passengers must be a positive integer")
		}
		if q.Origin == "" || q.Destination == "" || q.Origin == q.Destination || q.Date == "" && q.ArrivalDate == "" {
			return q, invalid("MISSING_QUERY_PARAMETERS: train query needs route and departure_date or arrival_date")
		}
		if q.Date != "" {
			if _, err = parseDate(q.Date); err != nil {
				return q, err
			}
		}
		if q.ArrivalDate != "" {
			if _, err = parseDate(q.ArrivalDate); err != nil {
				return q, err
			}
		}
		if q.ArrivalBefore != "" {
			if _, err = time.Parse(time.RFC3339, q.ArrivalBefore); err != nil {
				return q, invalid("arrival_before requires timezone")
			}
		}
	case "hotel":
		q.City = textValue(p, "city")
		if q.City == "" {
			q.City = textValue(p, "destination")
		}
		q.CheckIn = textValue(p, "check_in")
		q.CheckOut = textValue(p, "check_out")
		q.Keywords = textValue(p, "keywords")
		q.Guests = intValue(p, "guests", 1)
		if !missingValue(p["guests"]) && intValue(p, "guests", 0) == 0 {
			return q, invalid("guests must be a positive integer")
		}
	case "weather":
		q.City = textValue(p, "city")
		q.Date = textValue(p, "date")
	case "web":
		q.Keywords = textValue(p, "query")
	}
	if item.Domain != "train" {
		if err = normalizeQuery(&q); err != nil {
			return q, err
		}
	}
	return q, nil
}
