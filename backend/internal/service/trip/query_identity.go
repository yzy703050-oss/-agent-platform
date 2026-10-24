package trip

import (
	"encoding/json"
	"time"

	"agent-platform/backend/internal/model"
)

func PhysicalQuery(q model.TravelQuery) model.TravelQuery {
	q.PlanID = ""
	q.PlanRevision = 0
	q.TaskID = ""
	q.TaskRevision = 0
	q.OriginalParameters = nil
	q.Constraints = model.TripConstraints{}
	q.Ranking = nil
	q.Refresh = false
	q.DependsOn = nil
	if q.Domain == "hotel" {
		q.CheckIn = ""
		q.CheckOut = ""
		q.Guests = 0
	} // Current hotel provider returns places, not inventory.
	return q
}

// ExternalQueryKeys reserves actual supplier requests before executing a batch.
// An arrival search can consume up to three departure-date requests.
func ExternalQueryKeys(q model.TravelQuery) []string {
	q = PhysicalQuery(q)
	if q.Domain == "train" && q.ArrivalDate != "" && q.Date == "" {
		target, err := parseDate(q.ArrivalDate)
		if err == nil {
			keys := []string{}
			for i := 0; i < 3; i++ {
				day := q
				day.Date = target.AddDate(0, 0, -i).Format(time.DateOnly)
				day.ArrivalDate = ""
				day.ArrivalBefore = ""
				keys = append(keys, ExternalQueryKeys(day)...)
			}
			return keys
		}
	}
	data, _ := json.Marshal(q)
	return []string{string(data)}
}
