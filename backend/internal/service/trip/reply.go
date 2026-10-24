package trip

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-platform/backend/internal/model"
)

// BindReply binds a response to the exact committed result. It cannot bless an
// uncommitted preview or a receipt for another business revision.
func BindReply(result model.TripResult, spec model.TripReplySpec) (model.TripBoundReply, error) {
	if result.Operation != "select" && result.Operation != "amend" {
		return model.TripBoundReply{}, invalid("INVALID_REPLY_REFERENCE: final reply requires select/amend")
	}
	if result.RouteChanged && len(spec.Tasks) > 0 {
		return model.TripBoundReply{}, invalid("INVALID_REPLY_REFERENCE: route changes require the full saved plan")
	}
	p := result.Plan
	r := result.Receipt
	if r == nil || r.OperationID == "" || r.PlanID != p.ID || r.Revision != p.Revision || p.ID == "" {
		return model.TripBoundReply{}, invalid("INVALID_REPLY_REFERENCE: reply requires the actual saved revision")
	}
	text, err := RenderReply(p, result.Validation, spec)
	return model.TripBoundReply{PlanID: p.ID, Revision: p.Revision, Text: text}, err
}

// RenderReply also supports read-only rendering of a stopped, already saved
// snapshot. Callers must not use it as evidence that a new edit was committed.
func RenderReply(p model.TripPlan, validation model.TripValidation, spec model.TripReplySpec) (string, error) {
	if utf8.RuneCountInString(spec.Explanation) > 600 || strings.IndexFunc(spec.Explanation, unicode.IsDigit) >= 0 {
		return "", invalid("INVALID_REPLY_REFERENCE: explanation must contain no numerical facts and be at most 600 characters")
	}
	for id, parts := range spec.Tasks {
		if len(parts) == 0 || slices.IndexFunc(p.Tasks, func(t model.TripTask) bool { return t.ID == id }) < 0 {
			return "", invalid("INVALID_REPLY_REFERENCE: unknown task or empty component list")
		}
		seen := map[string]bool{}
		for _, part := range parts {
			if seen[part] || !slices.Contains([]string{"train", "hotel", "content"}, part) {
				return "", invalid("INVALID_REPLY_REFERENCE: invalid or repeated reply component")
			}
			seen[part] = true
		}
	}
	lines := []string{"已保留以下安排，仍有待处理问题："}
	if validation.Status == "complete" {
		lines[0] = "已保存以下安排："
	}
	var known int64
	shownTrain, allKnown, anyKnown, isDemo := false, true, false, false
	for _, t := range p.Tasks {
		parts, requested := spec.Tasks[t.ID]
		if len(spec.Tasks) == 0 {
			parts = []string{"train", "hotel", "content"}
			requested = true
		}
		if !requested {
			continue
		}
		section := []string{}
		if slices.Contains(parts, "train") {
			shownTrain = true
			if t.Train == nil {
				section = append(section, "车次与票价待核实。")
				allKnown = false
			} else {
				c := t.Train.Candidate
				isDemo = isDemo || c.IsDemo
				fare := "票价未核实"
				if c.PriceFen != nil && *c.PriceFen >= 0 {
					fare = moneyText(*c.PriceFen) + "元"
					party := int64(max(1, t.Passengers))
					if *c.PriceFen > math.MaxInt64/party {
						return "", invalid("reply party price overflow")
					}
					amount := *c.PriceFen * party
					if party != 1 {
						fare += fmt.Sprintf("/人，%d人", party)
					}
					if known > math.MaxInt64-amount {
						return "", invalid("reply monetary total overflow")
					}
					known += amount
					anyKnown = true
				} else {
					allKnown = false
				}
				depart, arrive := "时刻未核实", "时刻未核实"
				if c.DepartureAt != nil {
					depart = c.DepartureAt.In(beijing).Format("2006-01-02 15:04")
				}
				if c.ArrivalAt != nil {
					arrive = c.ArrivalAt.In(beijing).Format("2006-01-02 15:04")
				}
				seat := c.SeatClass
				if seat == "" {
					seat = "席别未核实"
				}
				section = append(section, fmt.Sprintf("%s，%s出发，%s抵达，%s，%s。", c.Name, depart, arrive, seat, fare))
			}
		}
		if slices.Contains(parts, "hotel") && (t.Hotel != nil || t.RequiresHotel) {
			name, quote := "酒店待核实", "房价与房态未核实"
			if t.Hotel != nil {
				name = t.Hotel.Candidate.Name
				isDemo = isDemo || t.Hotel.Candidate.IsDemo
				if t.Hotel.Candidate.PriceFen != nil {
					quote = "住宿报价" + moneyText(*t.Hotel.Candidate.PriceFen) + "元"
				}
			}
			stay := "，入住退房日期待核实"
			if t.CheckIn != "" && t.CheckOut != "" {
				stay = "，" + t.CheckIn + "入住、" + t.CheckOut + "退房"
			}
			section = append(section, "住宿："+name+stay+"；"+quote+"。")
		}
		if slices.Contains(parts, "content") {
			for _, a := range t.Activities {
				day := a.Date
				if day == "" {
					day = "日期待定"
				}
				detail := ""
				if a.Description != "" && a.Description != a.Purpose {
					detail = "；" + a.Description
				}
				section = append(section, "活动："+day+" "+a.Time+" "+a.Purpose+detail+"。")
			}
		}
		if len(section) > 0 {
			lines = append(lines, t.Origin+"→"+t.Destination+"：\n"+strings.Join(section, "\n"))
		}
	}
	if shownTrain {
		if !anyKnown {
			lines = append(lines, "交通费用待核实（不含住宿）。")
		} else {
			label := "已知交通费用"
			if allKnown {
				label = "交通小计"
			}
			lines = append(lines, label+"："+moneyText(known)+"元（不含住宿）。")
		}
	}
	problems := []string{}
	policy := false
	for _, issue := range validation.Issues {
		if !issue.Blocking {
			continue
		}
		if strings.HasPrefix(issue.Code, "policy_") {
			policy = true
			continue
		}
		if !slices.Contains(problems, issue.Message) {
			problems = append(problems, issue.Message)
		}
	}
	if policy {
		lines = append(lines, "企业政策仍有未核实事项，不能确认合规完成。")
	}
	if len(problems) > 0 {
		lines = append(lines, "待处理："+strings.Join(problems[:min(5, len(problems))], "；"))
	}
	if strings.TrimSpace(spec.Explanation) != "" {
		lines = append(lines, strings.TrimSpace(spec.Explanation))
	}
	if isDemo {
		lines = append([]string{"演示数据，仅用于展示，不代表真实车次、报价或房态。"}, lines...)
	}
	return strings.Join(lines, "\n\n"), nil
}

func moneyText(fen int64) string {
	if fen%100 == 0 {
		return fmt.Sprintf("%d", fen/100)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%02d", fen/100, fen%100), "0")
}
