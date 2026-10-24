package trip

import (
	"fmt"
	"time"

	"agent-platform/backend/internal/model"
)

func sourceHardDateConflict(p model.TripPlan) *model.TripIssue {
	makeIssue := func(tasks []model.TripTask, message string, refs []string) *model.TripIssue {
		ids := []string{}
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		return &model.TripIssue{Code: "hard_date_conflict", TaskIDs: ids, Message: message, Blocking: true, Evidence: rawValue(refs)}
	}
	globalHard := func(k string) bool {
		return !missingValue(conditionLeaf(p.ConfirmedConditions, k)) && p.ConditionStrengths[k] != model.Soft
	}
	if globalHard("start_date") && globalHard("end_date") && textValue(p.ConfirmedConditions, "start_date") > textValue(p.ConfirmedConditions, "end_date") {
		return makeIssue(p.Tasks, "指定出发日晚于最晚返回日。", nil)
	}
	for index, t := range p.Tasks {
		c := effectiveConditions(p.ConfirmedConditions, t.Conditions)
		departure := textValue(c, "departure_date")
		if departure == "" && index == 0 {
			departure = textValue(p.ConfirmedConditions, "start_date")
		}
		hardDeparture := departure != "" && sourceHard(p, t, "departure_date")
		deadline, arrivalDay := textValue(c, "arrival_before"), textValue(c, "arrival_date")
		hardDeadline := deadline != "" && sourceHard(p, t, "arrival_before")
		hardArrival := arrivalDay != "" && sourceHard(p, t, "arrival_date")
		if hardDeparture {
			target := ""
			if hardDeadline {
				target = deadline[:10]
			}
			if hardArrival && (target == "" || arrivalDay < target) {
				target = arrivalDay
			}
			if target != "" && departure > target {
				return makeIssue([]model.TripTask{t}, fmt.Sprintf("指定出发日 %s 晚于要求抵达日 %s。", departure, target), nil)
			}
		}
		in, out := textValue(c, "check_in"), textValue(c, "check_out")
		if in != "" && out != "" && sourceHard(p, t, "check_in") && sourceHard(p, t, "check_out") && in >= out {
			return makeIssue([]model.TripTask{t}, "固定离店日期不晚于固定入住日期。", nil)
		}
		if index > 0 && hardDeparture {
			previous := p.Tasks[index-1]
			checkout := textValue(previous.Conditions, "check_out")
			if checkout != "" && sourceHard(p, previous, "check_out") && departure < checkout {
				return makeIssue([]model.TripTask{previous, t}, fmt.Sprintf("固定下一段出发日 %s 早于固定离店日 %s。", departure, checkout), nil)
			}
		}
		if !hardDeparture {
			continue
		}
		hardEnd := index == len(p.Tasks)-1 && globalHard("end_date")
		activities := []model.TripActivity{}
		for _, a := range t.Activities {
			if a.Strength == model.Hard {
				activities = append(activities, a)
			}
		}
		if !hardDeadline && !hardArrival && !hardEnd && len(activities) == 0 {
			continue
		}
		candidates := []model.TravelCandidate{}
		refs := []string{}
		for id, e := range p.QueryResults {
			q := e.Query
			if q.Domain != "train" || e.TaskID != t.ID || q.TaskRevision != t.Revision || (e.Status != "ok" && e.Status != "partial") || q.Origin != t.Origin || q.Destination != t.Destination || q.Date != departure || max(q.Passengers, 1) != max(t.Passengers, 1) {
				continue
			}
			refs = append(refs, id)
			candidates = append(candidates, e.Candidates...)
		}
		if len(candidates) == 0 {
			continue
		}
		known := true
		for _, candidate := range candidates {
			if candidate.ArrivalAt == nil || candidate.DepartureAt == nil || !candidate.ArrivalAt.After(*candidate.DepartureAt) || candidate.DepartureAt.In(beijing).Format("2006-01-02") != departure {
				known = false
				break
			}
		}
		if !known {
			continue
		}
		possible := false
		for _, candidate := range candidates {
			at := candidate.ArrivalAt.In(beijing)
			within := true
			if hardDeadline {
				bound, _ := time.Parse(time.RFC3339, deadline)
				within = within && !at.After(bound)
			}
			within = within && (!hardArrival || at.Format("2006-01-02") == arrivalDay) && (!hardEnd || at.Format("2006-01-02") <= textValue(p.ConfirmedConditions, "end_date"))
			possible = possible || within
		}
		for _, a := range activities {
			allLate := a.Date != ""
			for _, candidate := range candidates {
				at := candidate.ArrivalAt.In(beijing)
				minutes := float64(at.Hour()*60+at.Minute()) + float64(at.Second())/60
				end := -1
				exact := sourceClock.FindStringSubmatch(a.Time)
				if exact != nil {
					clock, _ := time.Parse("15:04", a.Time)
					end = clock.Hour()*60 + clock.Minute()
				} else {
					switch a.Time {
					case "上午":
						end = 720
					case "下午":
						end = 1080
					case "晚上":
						end = 1440
					}
				}
				late := a.Date < at.Format("2006-01-02") || a.Date == at.Format("2006-01-02") && end >= 0 && (exact != nil && minutes > float64(end) || exact == nil && minutes >= float64(end))
				allLate = allLate && late
			}
			if allLate {
				return makeIssue([]model.TripTask{t}, fmt.Sprintf("本次已查车次按固定日期 %s 出发均晚于硬活动 %s %s，无法衔接。", departure, a.Date, a.Time), refs)
			}
		}
		if !possible {
			bound := deadline
			if bound == "" {
				bound = arrivalDay
			}
			if bound == "" {
				bound = textValue(p.ConfirmedConditions, "end_date")
			}
			return makeIssue([]model.TripTask{t}, fmt.Sprintf("本次已查车次按固定日期 %s 出发均无法满足抵达期限 %s。", departure, bound), refs)
		}
	}
	return nil
}
