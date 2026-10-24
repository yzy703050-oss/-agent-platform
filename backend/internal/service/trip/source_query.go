package trip

import (
	"slices"
	"strings"

	"agent-platform/backend/internal/model"
)

func sourceQueryRequests(p model.TripPlan, cmd model.TripCommand) ([]model.TravelQuery, error) {
	if sourceHardDateConflict(p) != nil {
		return nil, nil
	}
	requested := map[string][]model.TripQueryItem{}
	if cmd.Operation == "propose" {
		for _, t := range p.Tasks {
			requested[t.ID] = t.Items
		}
	} else if cmd.RouteTasks != nil {
		for i, row := range *cmd.RouteTasks {
			if row.Items != nil {
				requested[p.Tasks[i].ID] = *row.Items
			}
		}
	} else {
		for _, change := range cmd.Changes {
			if change.Items != nil {
				requested[change.TaskID] = *change.Items
			}
		}
	}
	queries := []model.TravelQuery{}
	for index, t := range p.Tasks {
		for _, item := range requested[t.ID] {
			if item.Domain != "train" && item.Domain != "hotel" {
				return nil, invalid("trip items only support train and hotel")
			}
			if item.Domain == "hotel" && !t.RequiresHotel {
				return nil, invalid("hotel outside task scope")
			}
			if err := validateSourceFilters(item.Constraints); err != nil {
				return nil, err
			}
			c := effectiveConditions(p.ConfirmedConditions, t.Conditions)
			inherited := filterValues(c)
			constraints := mergeConditions(nil, item.Constraints)
			levels := map[string]model.ConstraintStrength{}
			for key, level := range item.ConditionStrengths {
				levels[key] = level
			}
			for key, value := range inherited {
				if key == "total_budget_cny" {
					continue
				}
				level := t.ConditionStrengths[key]
				if level == "" {
					level = p.ConditionStrengths[key]
				}
				if level == model.Soft {
					continue
				}
				if item.ConditionStrengths[key] == model.Soft {
					return nil, invalid("QUERY_CONDITION_CONFLICT: item cannot soften inherited hard constraint: " + key)
				}
				if given, ok := constraints[key]; ok && !semanticSourceEqual(given, value) {
					return nil, invalid("QUERY_CONDITION_CONFLICT: item cannot relax inherited constraint: " + key)
				}
				constraints[key] = value
			}
			filters, err := sourceConstraints(constraints, levels)
			if err != nil {
				return nil, err
			}
			filters.MaxBudgetFen = nil
			q := model.TravelQuery{SourceVersion: 1, Domain: item.Domain, PlanID: p.ID, PlanRevision: p.Revision, TaskID: t.ID, TaskRevision: t.Revision, Constraints: filters, Passengers: t.Passengers, Guests: t.Guests, Ranking: item.Ranking, Refresh: item.Refresh, DependsOn: slices.Clone(item.DependsOn)}
			for i, dep := range q.DependsOn {
				if dep == "previous" {
					if index == 0 {
						return nil, invalid("first task has no previous dependency")
					}
					q.DependsOn[i] = p.Tasks[index-1].ID
				} else if slices.IndexFunc(p.Tasks, func(t model.TripTask) bool { return t.ID == dep }) < 0 {
					return nil, invalid("unknown query dependency")
				}
			}
			params := item.Parameters
			if item.Domain == "train" {
				q.Origin = t.Origin
				q.Destination = t.Destination
				q.Date = t.DepartureDate
				q.ArrivalDate = t.ArrivalDate
				q.ArrivalBefore = t.ArrivalBefore
				for key, want := range map[string]string{"origin": t.Origin, "destination": t.Destination} {
					if got := textValue(params, key); got != "" && got != want {
						return nil, invalid("query conflicts with task route: " + key)
					}
				}
				for key, dest := range map[string]*string{"departure_date": &q.Date, "arrival_date": &q.ArrivalDate, "arrival_before": &q.ArrivalBefore} {
					if value := textValue(params, key); value != "" {
						if sourceHard(p, t, key) && *dest != "" && value != *dest {
							return nil, invalid("query conflicts with hard condition: " + key)
						}
						*dest = value
					}
				}
				if params["passengers"] != nil && intValue(params, "passengers", 0) != t.Passengers {
					return nil, invalid("query conflicts with task party")
				}
			} else {
				q.City = t.Destination
				q.CheckIn = t.CheckIn
				q.CheckOut = t.CheckOut
				q.Keywords = t.HotelKeywords
				if city := textValue(params, "city"); city != "" && city != t.Destination {
					return nil, invalid("query conflicts with task city")
				}
				for key, dest := range map[string]*string{"check_in": &q.CheckIn, "check_out": &q.CheckOut} {
					if value := textValue(params, key); value != "" {
						if sourceHard(p, t, key) && *dest != "" && value != *dest {
							return nil, invalid("query conflicts with hard stay condition: " + key)
						}
						*dest = value
					}
				}
				if value := textValue(params, "keywords"); value != "" {
					q.Keywords = value
				}
				if params["guests"] != nil && intValue(params, "guests", 0) != t.Guests {
					return nil, invalid("query conflicts with hotel party")
				}
			}
			// Missing route/date is a real dependency, not a reason to invent one.
			if q.Domain == "train" && (q.Origin == "" || q.Destination == "" || (q.Date == "" && q.ArrivalDate == "" && q.ArrivalBefore == "")) {
				continue
			}
			if strings.TrimSpace(q.City) == "" && q.Domain == "hotel" {
				continue
			}
			q.OriginalParameters = mergeConditions(nil, item.Parameters)
			queries = append(queries, q)
		}
	}
	return queries, ValidateQueryDependencies(queries)
}
