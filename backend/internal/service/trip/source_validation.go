package trip

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"time"

	"agent-platform/backend/internal/model"
)

var sourceClock = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func validateSourceTimeline(p model.TripPlan, v *model.TripValidation) error {
	add := func(code string, ids []string, message string, blocking bool, activity *model.TripActivity) {
		issue := model.TripIssue{Code: code, TaskIDs: ids, Message: message, Blocking: blocking}
		if len(ids) > 0 {
			issue.TaskID = ids[0]
		}
		if activity != nil {
			issue.Evidence = rawValue(activity.ID)
			issue.RepairPermissions = map[string]bool{"automatic_activity_edit": activity.Strength != model.Hard}
		}
		v.Issues = append(v.Issues, issue)
		if blocking && v.Status == "complete" {
			v.Status = "partial"
		}
	}
	for index, t := range p.Tasks {
		var arrival, next *time.Time
		if t.Train != nil {
			arrival = t.Train.Candidate.ArrivalAt
		}
		if index+1 < len(p.Tasks) && p.Tasks[index+1].Train != nil {
			next = p.Tasks[index+1].Train.Candidate.DepartureAt
		}
		if index > 0 && t.Train != nil && t.Train.Candidate.DepartureAt != nil {
			previous := p.Tasks[index-1]
			dep := t.Train.Candidate.DepartureAt
			if previous.Train != nil && previous.Train.Candidate.ArrivalAt != nil && dep.Before(*previous.Train.Candidate.ArrivalAt) {
				add("departure_before_previous_arrival", []string{previous.ID, t.ID}, "下一段在前段抵达之前出发。", true, nil)
			}
			if previous.CheckOut != "" && dep.In(beijing).Format("2006-01-02") < previous.CheckOut {
				add("departure_before_previous_checkout", []string{previous.ID, t.ID}, "下一段在前段离店日期之前出发。", true, nil)
			}
		}
		if t.Train != nil && t.Train.Candidate.DepartureAt != nil {
			depart := t.Train.Candidate.DepartureAt.In(beijing)
			if start := textValue(p.ConfirmedConditions, "start_date"); index == 0 && start != "" && p.ConditionStrengths["start_date"] != model.Soft && depart.Format("2006-01-02") != start {
				add("start_date_conflict", []string{t.ID}, "实际出发日期与固定行程起始日不符。", true, nil)
			}
			if t.Constraints.DepartureTimeAfter != "" && depart.Format("15:04") < t.Constraints.DepartureTimeAfter {
				add("train_time_constraint_failed", []string{t.ID}, "发车时间早于硬时间条件。", true, nil)
			}
			if t.Constraints.DepartureTimeBefore != "" && depart.Format("15:04") > t.Constraints.DepartureTimeBefore {
				add("train_time_constraint_failed", []string{t.ID}, "发车时间晚于硬时间条件。", true, nil)
			}
		}
		if index == len(p.Tasks)-1 && arrival != nil {
			if end := textValue(p.ConfirmedConditions, "end_date"); end != "" && p.ConditionStrengths["end_date"] != model.Soft && arrival.In(beijing).Format("2006-01-02") > end {
				add("end_date_conflict", []string{t.ID}, "实际抵达日期晚于固定最晚返回日。", true, nil)
			}
		}
		if t.RequiresHotel && arrival != nil && t.CheckIn != "" && t.CheckIn < arrival.In(beijing).Format("2006-01-02") {
			add("hotel_before_arrival", []string{t.ID}, "入住日期早于火车实际抵达日期。", true, nil)
		}
		if t.Constraints.MaxTrainFen != nil && t.Train != nil && t.Train.Candidate.PriceFen != nil {
			price := *t.Train.Candidate.PriceFen
			party := int64(max(t.Passengers, 1))
			if price < 0 || price > math.MaxInt64/party {
				return invalid("party price overflow")
			}
			if price*party > *t.Constraints.MaxTrainFen {
				add("train_budget_failed", []string{t.ID}, "车票费用不符合硬预算。", true, nil)
			}
		}
		if t.Hotel != nil && t.Hotel.Candidate.PriceFen != nil {
			price := *t.Hotel.Candidate.PriceFen
			if t.Constraints.MaxHotelTotalFen != nil && price > *t.Constraints.MaxHotelTotalFen {
				add("hotel_budget_failed", []string{t.ID}, "住宿费用不符合预算。", true, nil)
			}
			if t.Constraints.MaxHotelNightFen != nil && t.Nights > 0 && new(big.Int).SetInt64(price).Cmp(new(big.Int).Mul(big.NewInt(*t.Constraints.MaxHotelNightFen), big.NewInt(int64(t.Nights)))) > 0 {
				add("hotel_budget_failed", []string{t.ID}, "住宿每晚费用不符合预算。", true, nil)
			}
		}
		for _, a := range t.Activities {
			if a.Date == "" {
				continue
			}
			minute := -1
			exact := sourceClock.FindStringSubmatch(a.Time)
			if exact != nil {
				h, _ := strconv.Atoi(exact[1])
				m, _ := strconv.Atoi(exact[2])
				minute = h*60 + m
			}
			start, end := minute, minute
			switch a.Time {
			case "上午":
				start = 0
				end = 720
			case "下午":
				start = 720
				end = 1080
			case "晚上":
				start = 1080
				end = 1440
			}
			code := ""
			overlap := false
			if arrival != nil {
				stamp := arrival.In(beijing)
				minutes := float64(stamp.Hour()*60+stamp.Minute()) + float64(stamp.Second())/60
				if a.Date < stamp.Format("2006-01-02") || a.Date == stamp.Format("2006-01-02") && end >= 0 && (exact != nil && minutes > float64(end) || exact == nil && minutes >= float64(end)) {
					code = "activity_before_arrival"
				} else if a.Date == stamp.Format("2006-01-02") && start >= 0 && exact == nil {
					overlap = float64(start) < minutes && minutes < float64(end)
				}
			}
			if next != nil {
				stamp := next.In(beijing)
				minutes := stamp.Hour()*60 + stamp.Minute()
				if a.Date > stamp.Format("2006-01-02") || a.Date == stamp.Format("2006-01-02") && start >= minutes && start >= 0 {
					code = "activity_after_departure"
				} else if a.Date == stamp.Format("2006-01-02") && start >= 0 && exact == nil {
					overlap = overlap || (start < minutes && minutes < end)
				}
			}
			if code == "" && overlap {
				code = "activity_window_unverified"
			}
			if code != "" {
				message := fmt.Sprintf("活动时段与实际交通不衔接。content是抵达目的地%s后的活动，不记录该段乘车或出发前退房。软活动可修改或删除，硬活动须保留并调整允许的软交通。", t.Destination)
				if code == "activity_window_unverified" {
					message = "content.time的粗时段与实际交通部分重叠，尚未完全核实；只改description不会改变time的区间解释。请结合抵达后/离开前的安排判断，合理安排无需为通过校验虚构精确时刻，保留用户硬活动原要求。"
				}
				add(code, []string{t.ID}, message, code != "activity_window_unverified", &a)
				issue := &v.Issues[len(v.Issues)-1]
				copyActivity := a
				issue.Activity = &copyActivity
				issue.ActivityScope = map[string]string{"destination": t.Destination, "kind": "destination_activity"}
				issue.TravelWindow = map[string]*time.Time{"arrival_at": arrival, "departure_at": next}
				if code == "activity_window_unverified" {
					issue.CoarseWindow = map[string]string{"start": fmt.Sprintf("%02d:%02d", start/60, start%60), "end": fmt.Sprintf("%02d:%02d", end/60, end%60)}
				}
			}
		}
	}
	if conflict := sourceHardDateConflict(p); conflict != nil {
		v.Issues = append(v.Issues, *conflict)
		if v.Status == "complete" {
			v.Status = "partial"
		}
	}
	return nil
}
