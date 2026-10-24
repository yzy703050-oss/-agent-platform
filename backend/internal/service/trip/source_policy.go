package trip

import (
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
)

// ValidateSourcePolicy is deterministic: extraction belongs to the Agent and
// actual retrieval references are authoritative over model-written citations.
func ValidateSourcePolicy(raw json.RawMessage, context model.TripConditions, references []model.SourcePolicyReference) (model.SourcePolicyContext, error) {
	var value model.SourcePolicyContext
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &value) != nil {
		return value, invalid("invalid policy JSON")
	}
	for _, key := range []string{"status", "constraints", "applicability", "references", "effective_date", "missing_fields", "conflicts"} {
		if _, ok := fields[key]; !ok {
			return value, invalid("missing policy field: " + key)
		}
	}
	if value.Applicability == nil || value.Constraints == nil || value.References == nil || value.MissingFields == nil || value.Conflicts == nil || !slices.Contains([]string{"ok", "partial", "error", "no_knowledge"}, value.Status) {
		return value, invalid("invalid policy structure")
	}
	ids := map[string]bool{}
	for _, ref := range references {
		if ref.ID != "" {
			ids[ref.ID] = true
		}
	}
	for _, constraint := range value.Constraints {
		if constraint.ID == "" || constraint.Rule == "" || !constraint.Hard || len(constraint.EvidenceIDs) == 0 {
			return value, invalid("invalid policy constraint or citation")
		}
		for _, id := range constraint.EvidenceIDs {
			if !ids[id] {
				return value, invalid("policy citation is not retrieved evidence")
			}
		}
		for key, expected := range constraint.Applicability {
			if !semanticSourceEqual(context[key], expected) {
				value.MissingFields = append(value.MissingFields, "policy_applicability:"+key)
			}
		}
		switch constraint.Field {
		case "hotel_max_nightly_cny", "hotel_max_total_cny", "train_max_total_cny":
			unit := "CNY"
			if constraint.Field == "hotel_max_nightly_cny" {
				unit = "CNY/night"
			}
			if constraint.Operator != "<=" || constraint.Unit == nil || *constraint.Unit != unit {
				return value, invalid("invalid policy operator/unit")
			}
			if _, err := MoneyFen(constraint.Value); err != nil {
				return value, err
			}
		case "seat_class":
			var seat string
			if constraint.Operator != "==" || constraint.Unit != nil || json.Unmarshal(constraint.Value, &seat) != nil || seat == "" {
				return value, invalid("invalid policy seat rule")
			}
		default:
			value.MissingFields = append(value.MissingFields, "policy_rule:"+constraint.ID)
		}
	}
	if value.EffectiveDate != nil {
		if _, err := time.Parse(time.DateOnly, *value.EffectiveDate); err != nil {
			return value, invalid("invalid policy effective date")
		}
		when := textValue(context, "departure_date")
		if when == "" {
			when = textValue(context, "current_time")
			if len(when) >= 10 {
				when = when[:10]
			}
		}
		if when != "" && when < *value.EffectiveDate {
			value.MissingFields = append(value.MissingFields, "policy_effective_date")
		}
	}
	for _, gap := range value.MissingFields {
		if strings.TrimSpace(gap) == "" {
			return value, invalid("policy missing_fields must contain nonempty strings")
		}
	}
	if value.Summary == "" {
		rules := []string{}
		for _, r := range value.Constraints {
			rules = append(rules, r.Rule)
		}
		value.Summary = strings.Join(rules, "；")
		if value.Summary == "" {
			value.Summary = "政策尚无可执行约束，请检查资料缺口。"
		}
	}
	slices.Sort(value.MissingFields)
	value.MissingFields = slices.Compact(value.MissingFields)
	value.References = references
	if len(value.MissingFields) > 0 || len(value.Conflicts) > 0 {
		value.Status = "partial"
	}
	return value, nil
}

func ResolveSourcePolicyForPlan(policy model.SourcePolicyContext, p model.TripPlan, context model.TripConditions) (model.SourcePolicyContext, error) {
	value, err := clone(policy)
	if err != nil {
		return value, err
	}
	gaps := []string{}
	for _, gap := range value.MissingFields {
		if !strings.HasPrefix(gap, "policy_applicability:") && gap != "policy_effective_date" {
			gaps = append(gaps, gap)
		}
	}
	scopes := []model.TripConditions{value.Applicability}
	for _, rule := range value.Constraints {
		scopes = append(scopes, rule.Applicability)
	}
	for index, scope := range scopes {
		for key, expected := range scope {
			if key == "city" {
				allKnown, found := len(p.Tasks) > 0, false
				for _, task := range p.Tasks {
					allKnown = allKnown && task.Destination != ""
					if semanticSourceEqual(rawValue(task.Destination), expected) {
						found = true
					}
				}
				if !allKnown || index == 0 && !found {
					gaps = append(gaps, "policy_applicability:city")
				}
			} else if !semanticSourceEqual(context[key], expected) {
				gaps = append(gaps, "policy_applicability:"+key)
			}
		}
	}
	if value.EffectiveDate != nil {
		for _, task := range p.Tasks {
			day := task.DepartureDate
			if day == "" {
				day = task.ArrivalDate
			}
			if day == "" || day < *value.EffectiveDate {
				gaps = append(gaps, "policy_effective_date")
			}
		}
	}
	slices.Sort(gaps)
	value.MissingFields = slices.Compact(gaps)
	if value.MissingFields == nil {
		value.MissingFields = []string{}
	}
	if value.Status != "error" && value.Status != "no_knowledge" {
		value.Status = "ok"
		if len(gaps) > 0 || len(value.Conflicts) > 0 {
			value.Status = "partial"
		}
	}
	return value, nil
}

func validateSourcePolicyRules(p model.TripPlan, v *model.TripValidation) {
	if p.PolicyContext == nil {
		return
	}
	ids := map[string]bool{}
	for _, ref := range p.PolicyContext.References {
		ids[ref.ID] = true
	}
	for _, rule := range p.PolicyContext.Constraints {
		if !rule.Hard {
			continue
		}
		for _, task := range p.Tasks {
			if city := textValue(rule.Applicability, "city"); city != "" && city != task.Destination {
				continue
			}
			if strings.HasPrefix(rule.Field, "hotel_") && !task.RequiresHotel {
				continue
			}
			verified, failed := len(rule.EvidenceIDs) > 0, false
			for _, id := range rule.EvidenceIDs {
				verified = verified && ids[id]
			}
			var actual *big.Rat
			switch rule.Field {
			case "seat_class":
				var expected string
				if json.Unmarshal(rule.Value, &expected) != nil || task.Train == nil || task.Train.Candidate.SeatClass == "" {
					verified = false
				} else {
					failed = task.Train.Candidate.SeatClass != expected
				}
			case "train_max_total_cny":
				if task.Train != nil && task.Train.Candidate.PriceFen != nil {
					actual = new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(*task.Train.Candidate.PriceFen), big.NewInt(int64(max(task.Passengers, 1)))))
				}
			case "hotel_max_total_cny", "hotel_max_nightly_cny":
				if task.Hotel != nil && task.Hotel.Candidate.PriceFen != nil {
					actual = new(big.Rat).SetInt64(*task.Hotel.Candidate.PriceFen)
					if rule.Field == "hotel_max_nightly_cny" {
						in, ie := parseDate(task.CheckIn)
						out, oe := parseDate(task.CheckOut)
						nights := int64(out.Sub(in).Hours() / 24)
						if ie != nil || oe != nil || nights <= 0 {
							actual = nil
						} else {
							actual.Quo(actual, new(big.Rat).SetInt64(nights))
						}
					}
				}
			default:
				verified = false
			}
			if rule.Field != "seat_class" {
				expected, err := MoneyFen(rule.Value)
				if err != nil || actual == nil {
					verified = false
				} else {
					failed = actual.Cmp(new(big.Rat).SetInt64(expected)) > 0
				}
			}
			code, message := "", ""
			if !verified {
				code = "policy_constraint_unverified"
				message = "缺少验证企业强制约束的有效证据。"
			} else if failed {
				code = "policy_constraint_failed"
				message = "所选方案不符合企业强制约束。"
			}
			if code != "" {
				v.Issues = append(v.Issues, model.TripIssue{Code: code, TaskID: task.ID, TaskIDs: []string{task.ID}, Blocking: true, Message: message, Evidence: rawValue(rule.ID)})
				if v.Status == "complete" {
					v.Status = "partial"
				}
			}
		}
	}
}
