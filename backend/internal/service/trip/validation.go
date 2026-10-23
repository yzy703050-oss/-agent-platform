package trip

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
)

var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

func Preview(previous *model.TripPlan, cmd model.TripCommand, evidence []model.TravelEvidence, now time.Time) (model.TripResult, error) {
	var p model.TripPlan
	var err error
	if cmd.Operation == "propose" {
		if previous != nil || cmd.PlanID != "" || cmd.ExpectedRevision != 0 || len(cmd.Changes) > 0 || len(cmd.Selections) > 0 {
			return model.TripResult{}, invalid("invalid creation command")
		}
		p = model.TripPlan{Revision: 1, RoundTrip: true}
		p.Tasks, err = clone(cmd.Tasks)
		if err != nil {
			return model.TripResult{}, err
		}
		for i := range p.Tasks {
			if p.Tasks[i].Train != nil || p.Tasks[i].Hotel != nil {
				return model.TripResult{}, invalid("creation cannot invent selected facts")
			}
			p.Tasks[i].ID = fmt.Sprintf("t%d", i+1)
		}
	} else {
		if previous == nil || cmd.PlanID != previous.ID || cmd.ExpectedRevision != previous.Revision {
			return model.TripResult{}, model.ErrConflict
		}
		if cmd.Operation != "amend" && cmd.Operation != "select" {
			return model.TripResult{}, invalid("unknown trip operation")
		}
		if len(cmd.Tasks) > 0 {
			return model.TripResult{}, invalid("use explicit patches for existing tasks")
		}
		if cmd.Operation == "select" && (len(cmd.Changes) > 0 || cmd.Constraints != nil || cmd.RoundTrip != nil) {
			return model.TripResult{}, invalid("selection cannot rewrite conditions")
		}
		p, err = clone(*previous)
		if err != nil {
			return model.TripResult{}, err
		}
		p.Revision++
	}
	if cmd.RoundTrip != nil {
		p.RoundTrip = *cmd.RoundTrip
	}
	if cmd.Constraints != nil {
		if previous != nil {
			if err = protectConstraints(previous.Constraints, *cmd.Constraints); err != nil {
				return model.TripResult{}, err
			}
		}
		p.Constraints, err = clone(*cmd.Constraints)
		if err != nil {
			return model.TripResult{}, err
		}
		for i := range p.Tasks {
			if p.Tasks[i].Train != nil && !matchesTrain(p.Tasks[i].Train.Candidate, p.Constraints) {
				p.Tasks[i].Train = nil
			}
		}
	}
	if err = validateConstraints(p.Constraints); err != nil {
		return model.TripResult{}, err
	}
	seen := map[string]bool{}
	for _, change := range cmd.Changes {
		if seen[change.TaskID] {
			return model.TripResult{}, invalid("duplicate task patch")
		}
		seen[change.TaskID] = true
		index := slices.IndexFunc(p.Tasks, func(t model.TripTask) bool { return t.ID == change.TaskID })
		if index < 0 {
			return model.TripResult{}, model.ErrNotFound
		}
		if err = applyChange(&p.Tasks[index], change); err != nil {
			return model.TripResult{}, err
		}
	}
	seen = map[string]bool{}
	for _, selection := range cmd.Selections {
		key := selection.TaskID + ":" + selection.Domain
		if seen[key] {
			return model.TripResult{}, invalid("duplicate selection")
		}
		seen[key] = true
		index := slices.IndexFunc(p.Tasks, func(t model.TripTask) bool { return t.ID == selection.TaskID })
		if index < 0 {
			return model.TripResult{}, model.ErrNotFound
		}
		chosen, err := selectCandidate(p, p.Tasks[index], selection, evidence, now)
		if err != nil {
			return model.TripResult{}, err
		}
		if selection.Domain == "train" {
			p.Tasks[index].Train = &chosen
		} else {
			p.Tasks[index].Hotel = &chosen
		}
	}
	v, queries, err := validatePlan(p)
	if err != nil {
		return model.TripResult{}, err
	}
	p.Validation = v
	return model.TripResult{Plan: p, Validation: v, Queries: queries}, nil
}

func applyChange(task *model.TripTask, c model.TripTaskChange) error {
	old := *task
	for _, pair := range []struct {
		in  *string
		out *string
	}{{c.Origin, &task.Origin}, {c.Destination, &task.Destination}, {c.DepartureDate, &task.DepartureDate}, {c.ArrivalDate, &task.ArrivalDate}, {c.ArrivalBefore, &task.ArrivalBefore}, {c.CheckIn, &task.CheckIn}, {c.CheckOut, &task.CheckOut}} {
		if pair.in != nil {
			*pair.out = *pair.in
		}
	}
	if c.RequiresHotel != nil {
		task.RequiresHotel = *c.RequiresHotel
	}
	if c.Nights != nil {
		task.Nights = *c.Nights
	}
	if c.Constraints != nil {
		if err := protectConstraints(old.Constraints, *c.Constraints); err != nil {
			return err
		}
		value, err := clone(*c.Constraints)
		if err != nil {
			return err
		}
		task.Constraints = value
	}
	if c.Activities != nil {
		for _, activity := range old.Activities {
			if activity.Strength != model.Hard {
				continue
			}
			if !slices.ContainsFunc(*c.Activities, func(next model.TripActivity) bool {
				return activity.ID == next.ID && activity.Purpose == next.Purpose && activity.Date == next.Date && activity.Time == next.Time && next.Strength == model.Hard
			}) {
				return invalid("cannot remove a hard activity")
			}
		}
		values, err := clone(*c.Activities)
		if err != nil {
			return err
		}
		task.Activities = values
	}
	if c.FieldSources != nil {
		if task.FieldSources == nil {
			task.FieldSources = map[string]string{}
		}
		for key, source := range c.FieldSources {
			task.FieldSources[key] = source
		}
	}
	if old.Origin != task.Origin || old.Destination != task.Destination || old.DepartureDate != task.DepartureDate || old.ArrivalDate != task.ArrivalDate || old.ArrivalBefore != task.ArrivalBefore || !reflect.DeepEqual(old.Constraints, task.Constraints) {
		task.Train = nil
	}
	if old.Destination != task.Destination || old.CheckIn != task.CheckIn || old.CheckOut != task.CheckOut || old.Nights != task.Nights || old.RequiresHotel != task.RequiresHotel || !reflect.DeepEqual(old.Constraints, task.Constraints) {
		task.Hotel = nil
	}
	return nil
}

func validatePlan(p model.TripPlan) (model.TripValidation, []model.TravelQuery, error) {
	v := model.TripValidation{Status: "complete", Issues: []model.TripIssue{}}
	var queries []model.TravelQuery
	priority := map[string]int{"complete": 0, "partial": 1, "needs_input": 2, "conflict": 3}
	add := func(code, task, field, message, status string) {
		v.Issues = append(v.Issues, model.TripIssue{Code: code, TaskID: task, Field: field, Message: message, Blocking: true})
		if priority[status] > priority[v.Status] {
			v.Status = status
		}
	}
	if len(p.Tasks) == 0 {
		add("missing_tasks", "", "tasks", "请补充出行安排。", "needs_input")
	}
	var priorDate time.Time
	var total int64
	unknownPrice := false
	for i, t := range p.Tasks {
		if strings.TrimSpace(t.Origin) == "" || strings.TrimSpace(t.Destination) == "" || t.DepartureDate == "" {
			add("missing_conditions", t.ID, "route", "请补充出发地、目的地和日期。", "needs_input")
			continue
		}
		if t.Origin == t.Destination {
			return v, nil, invalid("origin equals destination")
		}
		departure, err := parseDate(t.DepartureDate)
		if err != nil {
			return v, nil, err
		}
		arrival := departure
		if t.ArrivalDate != "" {
			arrival, err = parseDate(t.ArrivalDate)
			if err != nil {
				return v, nil, err
			}
			if arrival.Before(departure) {
				return v, nil, invalid("arrival precedes departure")
			}
		}
		if !priorDate.IsZero() && departure.Before(priorDate) {
			add("date_conflict", t.ID, "departure_date", "后续行程日期早于上一段抵达日期。", "conflict")
		}
		if i > 0 && p.Tasks[i-1].Destination != t.Origin {
			add("route_gap", t.ID, "origin", "相邻行程之间缺少交通衔接。", "needs_input")
		}
		priorDate = arrival
		if err = validateConstraints(t.Constraints); err != nil {
			return v, nil, err
		}
		for _, source := range t.FieldSources {
			if !validSource(source) {
				return v, nil, invalid("invalid condition source")
			}
		}
		if t.ArrivalBefore != "" {
			if _, err = time.Parse(time.RFC3339, t.ArrivalBefore); err != nil {
				return v, nil, invalid("arrival deadline must include date and timezone")
			}
			source := t.FieldSources["arrival_before"]
			if source != "user" && source != "context" {
				return v, nil, invalid("inferred arrival deadline cannot be hard")
			}
		}
		if t.Train == nil {
			queries = append(queries, model.TravelQuery{Domain: "train", PlanID: p.ID, PlanRevision: p.Revision, TaskID: t.ID, Origin: t.Origin, Destination: t.Destination, Date: t.DepartureDate, Constraints: t.Constraints})
			add("train_pending", t.ID, "train", "尚未选择有效车次。", "partial")
		}
		if t.RequiresHotel {
			if t.CheckIn == "" || t.CheckOut == "" {
				add("missing_stay", t.ID, "check_in", "请补充入住和退房日期。", "needs_input")
			} else {
				in, err := parseDate(t.CheckIn)
				if err != nil {
					return v, nil, err
				}
				out, err := parseDate(t.CheckOut)
				if err != nil {
					return v, nil, err
				}
				nights := int(out.Sub(in) / (24 * time.Hour))
				if nights <= 0 || in.Before(arrival) || (t.Nights != 0 && t.Nights != nights) {
					return v, nil, invalid("invalid stay dates or nights")
				}
				if t.Hotel == nil {
					queries = append(queries, model.TravelQuery{Domain: "hotel", PlanID: p.ID, PlanRevision: p.Revision, TaskID: t.ID, City: t.Destination, CheckIn: t.CheckIn, CheckOut: t.CheckOut})
					add("hotel_pending", t.ID, "hotel", "尚未选择酒店地点。", "partial")
				}
			}
		}
		for j, a := range t.Activities {
			if strings.TrimSpace(a.Purpose) == "" || !validSource(a.Source) || (a.Strength != model.Hard && a.Strength != model.Soft) {
				return v, nil, invalid("invalid activity")
			}
			if a.Strength == model.Hard && a.Source != "user" && a.Source != "context" {
				return v, nil, invalid("proposed activity cannot be hard")
			}
			if a.Date != "" {
				date, err := parseDate(a.Date)
				if err != nil {
					return v, nil, err
				}
				if date.Before(departure) {
					add("activity_date_conflict", t.ID, "activities", "活动日期早于该段出行。", "conflict")
				}
			}
			if p.Tasks[i].Activities[j].ID == "" {
				p.Tasks[i].Activities[j].ID = fmt.Sprintf("%s-a%d", t.ID, j+1)
			}
		}
		var taskTotal int64
		taskUnknown := t.Train == nil || (t.RequiresHotel && t.Hotel == nil)
		for _, choice := range []*model.TravelSelection{t.Train, t.Hotel} {
			if choice == nil {
				continue
			}
			if choice.Candidate.PriceFen == nil {
				unknownPrice = true
				taskUnknown = true
				continue
			}
			value := *choice.Candidate.PriceFen
			if value < 0 || total > math.MaxInt64-value {
				return v, nil, invalid("invalid or overflowing price")
			}
			total += value
			taskTotal += value
		}
		unknownPrice = unknownPrice || taskUnknown
		if t.Constraints.MaxBudgetFen != nil {
			if taskTotal > *t.Constraints.MaxBudgetFen && t.Constraints.BudgetStrength != model.Soft {
				add("task_budget_exceeded", t.ID, "max_budget_fen", "该段已知金额超过硬预算。", "conflict")
			}
			if taskUnknown {
				add("task_budget_unknown", t.ID, "max_budget_fen", "该段价格不完整，无法确认预算。", "partial")
			}
		}
		if t.RequiresHotel && (t.Hotel == nil || t.Hotel.Candidate.PriceFen == nil) {
			add("hotel_price_unknown", t.ID, "hotel", "酒店地点信息不包含房价和库存。", "partial")
		}
	}
	if p.RoundTrip && len(p.Tasks) > 0 && p.Tasks[len(p.Tasks)-1].Destination != p.Tasks[0].Origin {
		add("return_missing", "", "round_trip", "往返行程需要明确返程段；单程请明确指定。", "needs_input")
	}
	if p.Constraints.MaxBudgetFen != nil {
		if total > *p.Constraints.MaxBudgetFen && p.Constraints.BudgetStrength != model.Soft {
			add("budget_exceeded", "", "max_budget_fen", "已知金额超过硬预算。", "conflict")
		}
		if unknownPrice {
			add("budget_unknown", "", "max_budget_fen", "价格不完整，无法确认总预算。", "partial")
		}
	}
	// Policies are verified separately using source-grounded evidence.
	add("policy_unverified", "", "policy", "差旅政策尚未核验。", "partial")
	if v.Status == "needs_input" || v.Status == "conflict" {
		queries = nil
	}
	return v, queries, nil
}

func parseDate(value string) (time.Time, error) {
	date, err := time.ParseInLocation("2006-01-02", value, beijing)
	if err != nil {
		return time.Time{}, invalid("invalid calendar date")
	}
	return date, nil
}
func validSource(source string) bool {
	return slices.Contains([]string{"user", "context", "preference", "derived", "default", "proposal"}, source)
}
