package trip

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
)

func applySourceContent(current, proposed []model.TripActivity) ([]model.TripActivity, error) {
	known := map[string]model.TripActivity{}
	for _, a := range current {
		known[a.ID] = a
	}
	seen := map[string]bool{}
	out := make([]model.TripActivity, 0, len(proposed))
	for _, a := range proposed {
		if !validSource(a.Source) || !slices.Contains([]model.ConstraintStrength{model.Hard, model.Soft}, a.Strength) || strings.TrimSpace(a.Purpose) == "" || strings.TrimSpace(a.Description) == "" || utf8.RuneCountInString(a.Purpose) > 160 || utf8.RuneCountInString(a.Description) > 1000 || utf8.RuneCountInString(a.Time) > 80 {
			return nil, invalid("invalid source activity")
		}
		if a.Date != "" {
			if _, err := parseDate(a.Date); err != nil {
				return nil, err
			}
		}
		if slices.Contains([]string{"preference", "default", "proposal"}, a.Source) {
			if a.Strength != model.Soft {
				return nil, invalid("model or memory activity proposals must be soft")
			}
			a.Source = "proposal"
		}
		if a.ID != "" {
			prior, ok := known[a.ID]
			if !ok || seen[a.ID] {
				return nil, invalid("activity identity is unknown or repeated")
			}
			if prior.Strength == model.Hard && a.Strength != model.Hard {
				return nil, invalid("cannot soften hard activity")
			}
			if prior.Time != "" && a.Time != "" && prior.Time != a.Time {
				if a.Purpose == prior.Purpose {
					a.Purpose = strings.ReplaceAll(a.Purpose, prior.Time, a.Time)
				}
				if a.Description == prior.Description {
					a.Description = strings.ReplaceAll(a.Description, prior.Time, a.Time)
				}
			}
		} else {
			a.ID = "activity_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	return out, nil
}

func mergeSourceItems(current, proposed []model.TripQueryItem) []model.TripQueryItem {
	domains := map[string]bool{}
	for _, item := range proposed {
		domains[item.Domain] = true
	}
	out := make([]model.TripQueryItem, 0, len(current)+len(proposed))
	for _, item := range current {
		if !domains[item.Domain] {
			out = append(out, item)
		}
	}
	return append(out, proposed...)
}

func legacyConditions(t model.TripTask) model.TripConditions {
	c := model.TripConditions{}
	for key, value := range map[string]string{"departure_date": t.DepartureDate, "arrival_date": t.ArrivalDate, "arrival_before": t.ArrivalBefore, "check_in": t.CheckIn, "check_out": t.CheckOut} {
		if value != "" {
			c[key] = rawValue(value)
		}
	}
	if t.Nights > 0 {
		c["nights"] = rawValue(t.Nights)
	}
	return c
}

func applySourceMetadata(p *model.TripPlan, previous *model.TripPlan, cmd model.TripCommand, now time.Time) error {
	if cmd.Context == nil {
		if p.SourceVersion > 0 {
			return invalid("native trip requires authorized decision context")
		}
		return nil
	}
	if !slices.Contains([]string{"main", "verifier"}, cmd.Context.Role) {
		return invalid("invalid trusted decision role")
	}
	if slices.Contains([]string{"pause", "cancel"}, cmd.Operation) && strings.TrimSpace(cmd.Reason) == "" {
		return invalid("stop reason is required")
	}
	p.SourceVersion = 1
	p.StopReason = ""
	p.StopExplanation = ""
	if len(cmd.RepairIssueCodes) > 0 {
		if previous == nil || cmd.Operation != "amend" || cmd.RouteTasks != nil || len(cmd.Changes) == 0 {
			return invalid("repair requires scoped amend tasks")
		}
		codes := map[string]bool{}
		related := map[string]bool{}
		for _, issue := range previous.Validation.Issues {
			if slices.Contains(cmd.RepairIssueCodes, issue.Code) {
				codes[issue.Code] = true
				for _, id := range issue.TaskIDs {
					related[id] = true
				}
				if issue.TaskID != "" {
					related[issue.TaskID] = true
				}
			}
		}
		for _, code := range cmd.RepairIssueCodes {
			if !codes[code] {
				return invalid("unrelated repair target or stale conflict")
			}
		}
		for _, change := range cmd.Changes {
			if !related[change.TaskID] {
				return invalid("unrelated repair target or stale conflict")
			}
		}
	}
	if err := ValidateConditions(cmd.ConfirmedConditions); err != nil {
		return err
	}
	if p.ConfirmedConditions == nil {
		p.ConfirmedConditions = model.TripConditions{}
	}
	for key, level := range cmd.ConditionStrengths {
		if level == model.Soft && !missingValue(conditionLeaf(p.ConfirmedConditions, key)) && p.ConditionStrengths[key] != model.Soft {
			return invalid("cannot soften hard global condition")
		}
	}
	p.ConfirmedConditions = mergeConditions(p.ConfirmedConditions, cmd.ConfirmedConditions)
	if p.ConditionStrengths == nil {
		p.ConditionStrengths = map[string]model.ConstraintStrength{}
	}
	for key, level := range cmd.ConditionStrengths {
		p.ConditionStrengths[key] = level
	}
	if p.FieldSources == nil {
		p.FieldSources = map[string]string{}
	}
	for key := range cmd.ConfirmedConditions {
		if p.FieldSources[key] == "" {
			p.FieldSources[key] = "user"
		}
	}
	for key, value := range cmd.FieldSources {
		p.FieldSources[key] = value
	}
	if cmd.Context.Policy != nil {
		value, err := clone(cmd.Context.Policy)
		if err != nil {
			return err
		}
		p.PolicyContext = value
	}
	if cmd.RouteTasks != nil {
		if cmd.Operation != "amend" || len(cmd.Changes) > 0 || len(*cmd.RouteTasks) == 0 {
			return invalid("full route is only valid for amend without partial tasks")
		}
		if err := replaceSourceRoute(p, *cmd.RouteTasks, cmd.Context.Role); err != nil {
			return err
		}
	}
	global, err := sourceConstraints(filterValues(p.ConfirmedConditions), p.ConditionStrengths)
	if err != nil {
		return err
	}
	p.Constraints = global
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if t.Conditions == nil {
			if previous != nil && previous.SourceVersion == 0 {
				t.Conditions = legacyConditions(*t)
			} else {
				t.Conditions = model.TripConditions{}
			}
		}
		if t.FieldSources == nil {
			t.FieldSources = map[string]string{}
		}
		if (cmd.Operation == "propose" || cmd.RouteTasks != nil) && boolValue(t.Conditions, "hotel_quote_required") {
			if slices.Contains([]string{"derived", "default", "proposal"}, t.FieldSources["hotel_quote_required"]) {
				return invalid("hotel quote requirement must come from explicit user, context or preference")
			}
			if cmd.Context.OriginalQuery != "" && cmd.Context.Preferences["hotel_quote_required"] != "true" && !sourceUserWantsQuote(cmd.Context.OriginalQuery) {
				return invalid("hotel quote requirement lacks explicit user or trusted preference evidence")
			}
		}
		if t.ConditionStrengths == nil {
			t.ConditionStrengths = map[string]model.ConstraintStrength{}
		}
		if t.Revision == 0 {
			t.Revision = 1
		}
		if t.Purpose == "" {
			t.Purpose = "unspecified"
		}
		if !slices.Contains([]string{"visit", "business", "return", "transit", "unspecified"}, t.Purpose) {
			return invalid("invalid task purpose")
		}
		if t.Origin == "" {
			if i > 0 {
				t.Origin = p.Tasks[i-1].Destination
			} else {
				t.Origin = cmd.Context.Preferences["home_location"]
			}
		}
		if strings.TrimSpace(t.Destination) == "" || t.Origin == t.Destination {
			return invalid("task route needs distinct cities")
		}
		if i > 0 && t.Origin != "" && t.Origin != p.Tasks[i-1].Destination {
			return invalid("route is not continuous")
		}
		if i == 0 && missingValue(t.Conditions["departure_date"]) && !missingValue(p.ConfirmedConditions["start_date"]) {
			t.Conditions["departure_date"] = slices.Clone(p.ConfirmedConditions["start_date"])
			t.FieldSources["departure_date"] = "derived"
		}
		for key := range t.Conditions {
			if t.FieldSources[key] == "" {
				t.FieldSources[key] = "proposal"
			}
		}
		for key, source := range t.FieldSources {
			if !validSource(source) {
				return invalid("invalid source for " + key)
			}
		}
		for key, level := range t.ConditionStrengths {
			if level != model.Hard && level != model.Soft {
				return invalid("invalid condition strength")
			}
			globalValue := conditionLeaf(p.ConfirmedConditions, key)
			if level == model.Soft && (!missingValue(globalValue) && p.ConditionStrengths[key] != model.Soft || key == "departure_date" && i == 0 && !missingValue(p.ConfirmedConditions["start_date"]) && p.ConditionStrengths["start_date"] != model.Soft) {
				return invalid("cannot soften inherited hard condition")
			}
		}
		globalLeaves := mergeConditions(p.ConfirmedConditions, filterValues(p.ConfirmedConditions))
		for key, value := range globalLeaves {
			if key == "constraints" || missingValue(value) || p.ConditionStrengths[key] == model.Soft {
				continue
			}
			local := conditionLeaf(t.Conditions, key)
			incompatible := !semanticSourceEqual(value, local)
			if strings.HasSuffix(key, "_cny") && !missingValue(local) {
				a, err := MoneyFen(value)
				if err != nil {
					return err
				}
				b, err := MoneyFen(local)
				if err != nil {
					return err
				}
				incompatible = b > a
			}
			if !missingValue(local) && incompatible {
				return invalid("hard global condition conflict: " + key)
			}
		}
		if err := projectSourceConditions(p, t); err != nil {
			return err
		}
		if (cmd.Operation == "propose" || cmd.RouteTasks != nil) && t.RequiresHotel && t.Nights <= 0 {
			return invalid("requires_hotel=true needs a model stay proposal: nights or check_in/check_out")
		}
		if cmd.Operation == "propose" {
			values, err := applySourceContent(nil, t.Activities)
			if err != nil {
				return err
			}
			t.Activities = values
		}
		if cmd.Operation == "propose" && t.DepartureDate != "" && t.DepartureDate < now.In(beijing).Format("2006-01-02") {
			return invalid("DEPARTURE_IN_PAST: departure_date is earlier than current date")
		}
		if previous != nil {
			j := slices.IndexFunc(previous.Tasks, func(old model.TripTask) bool { return old.ID == t.ID })
			if j >= 0 {
				old := previous.Tasks[j]
				for _, change := range cmd.Changes {
					if change.TaskID == t.ID {
						for key, level := range change.ConditionStrengths {
							if level == model.Soft && sourceHard(*previous, old, key) {
								return invalid("cannot soften hard task condition")
							}
						}
					}
				}
				trainChanged := old.Origin != t.Origin || old.Destination != t.Destination || old.DepartureDate != t.DepartureDate || old.ArrivalDate != t.ArrivalDate || old.ArrivalBefore != t.ArrivalBefore || old.Passengers != t.Passengers && !((old.Passengers == 0) && (t.Passengers == 1)) || !sameTrainConstraints(old.Constraints, t.Constraints)
				hotelChanged := old.Destination != t.Destination || old.RequiresHotel != t.RequiresHotel || old.CheckIn != t.CheckIn || old.CheckOut != t.CheckOut || old.Nights != t.Nights || old.Guests != t.Guests && !((old.Guests == 0) && (t.Guests == 1)) || old.HotelKeywords != t.HotelKeywords || !sameHotelConstraints(old.Constraints, t.Constraints)
				if trainChanged {
					t.Train = nil
				}
				if hotelChanged {
					t.Hotel = nil
				}
				if trainChanged || hotelChanged {
					t.Revision = max(old.Revision, 1) + 1
				}
			}
		}
	}
	snapshot, err := sourceConstraintSnapshot(*p)
	if err != nil {
		return err
	}
	if previous != nil && cmd.Context.Role == "verifier" {
		old, err := sourceConstraintSnapshot(*previous)
		if err != nil {
			return err
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(old, &before)
		_ = json.Unmarshal(snapshot, &after)
		if !semanticSourceEqual(before["global"], after["global"]) || !semanticSourceEqual(before["tasks"], after["tasks"]) {
			return invalid("REPLAN_REQUIRED: local review cannot change hard requirements, activities or route")
		}
	}
	revision := int64(1)
	if previous != nil {
		var oldRevision struct {
			Revision int64 `json:"revision"`
		}
		_ = json.Unmarshal(previous.ConstraintSnapshot, &oldRevision)
		revision = max(oldRevision.Revision, 1)
		old, _ := sourceConstraintSnapshot(*previous)
		if !semanticSourceEqual(old, snapshot) {
			revision++
		}
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(snapshot, &fields)
	fields["revision"] = rawValue(revision)
	snapshot = rawValue(fields)
	p.ConstraintSnapshot = snapshot
	return nil
}

func sameTrainConstraints(a, b model.TripConstraints) bool {
	a.MaxBudgetFen = nil
	b.MaxBudgetFen = nil
	a.MaxHotelNightFen = nil
	b.MaxHotelNightFen = nil
	a.MaxHotelTotalFen = nil
	b.MaxHotelTotalFen = nil
	a.BudgetStrength = ""
	b.BudgetStrength = ""
	if len(a.Strengths) == 0 {
		a.Strengths = nil
	}
	if len(b.Strengths) == 0 {
		b.Strengths = nil
	}
	return reflect.DeepEqual(a, b)
}
func sameHotelConstraints(a, b model.TripConstraints) bool {
	return reflect.DeepEqual(a.MaxHotelNightFen, b.MaxHotelNightFen) && reflect.DeepEqual(a.MaxHotelTotalFen, b.MaxHotelTotalFen)
}

func sourceConstraintSnapshot(p model.TripPlan) (json.RawMessage, error) {
	hardConditions := func(c model.TripConditions, isHard func(string) bool) model.TripConditions {
		out := model.TripConditions{}
		for key, v := range c {
			if key != "constraints" && isHard(key) {
				out[key] = v
			}
		}
		limits := model.TripConditions{}
		for key, v := range filterValues(c) {
			if isHard(key) {
				limits[key] = v
			}
		}
		if len(limits) > 0 {
			out["constraints"] = rawValue(limits)
		}
		return out
	}
	tasks := map[string]any{}
	for _, t := range p.Tasks {
		activities := []model.TripActivity{}
		for _, a := range t.Activities {
			if a.Strength == model.Hard {
				activities = append(activities, a)
			}
		}
		tasks[t.ID] = map[string]any{"route": map[string]string{"origin": t.Origin, "destination": t.Destination, "purpose": t.Purpose}, "conditions": hardConditions(t.Conditions, func(k string) bool { return sourceHard(p, t, k) }), "activities": activities}
	}
	return json.Marshal(map[string]any{"global": map[string]any{"conditions": hardConditions(p.ConfirmedConditions, func(k string) bool {
		return !missingValue(conditionLeaf(p.ConfirmedConditions, k)) && p.ConditionStrengths[k] != model.Soft
	})}, "tasks": tasks, "policy": p.PolicyContext})
}

func semanticSourceEqual(a, b json.RawMessage) bool {
	decode := func(data []byte) any {
		var v any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		if d.Decode(&v) != nil {
			return nil
		}
		return v
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			if r, ok := new(big.Rat).SetString(string(x)); ok {
				return r.RatString()
			}
		case map[string]any:
			for k, v := range x {
				x[k] = normalize(v)
			}
		case []any:
			for i, v := range x {
				x[i] = normalize(v)
			}
		}
		return v
	}
	return reflect.DeepEqual(normalize(decode(a)), normalize(decode(b)))
}

func replaceSourceRoute(p *model.TripPlan, rows []model.TripTaskChange, role string) error {
	used := map[string]bool{}
	out := make([]model.TripTask, 0, len(rows))
	for _, row := range rows {
		matches := []model.TripTask{}
		for _, t := range p.Tasks {
			if !used[t.ID] && ((row.TaskID != "" && row.TaskID == t.ID) || (row.TaskID == "" && row.Origin != nil && row.Destination != nil && *row.Origin == t.Origin && *row.Destination == t.Destination)) {
				matches = append(matches, t)
			}
		}
		if row.TaskID != "" && len(matches) != 1 {
			return invalid("route task identity unknown or repeated")
		}
		next := model.TripTask{ID: "task_" + strings.ReplaceAll(uuid.NewString(), "-", ""), Revision: 1}
		if len(matches) == 1 {
			next = matches[0]
		}
		if err := applyChange(&next, row, role); err != nil {
			return err
		}
		if used[next.ID] {
			return invalid("repeated route task")
		}
		used[next.ID] = true
		out = append(out, next)
	}
	p.Tasks = out
	return nil
}

func finishSourcePlan(p *model.TripPlan, cmd model.TripCommand) {
	p.Status = "running"
	if slices.Contains([]string{"pause", "cancel"}, cmd.Operation) {
		p.StopReason = map[string]string{"pause": "paused", "cancel": "cancelled"}[cmd.Operation]
		p.StopExplanation = cmd.Reason
		return
	}
	if p.Validation.Status == "complete" && slices.Contains([]string{"select", "amend", "confirm"}, cmd.Operation) {
		p.Status = "completed"
		if cmd.Operation == "confirm" {
			p.UserConfirmed = true
		}
		return
	}
	p.StopReason = "awaiting_model"
	policyOnly := true
	blocking := 0
	for _, issue := range p.Validation.Issues {
		if issue.Blocking {
			blocking++
			if issue.Code != "policy_unverified" && issue.Code != "policy_constraint_unverified" {
				policyOnly = false
			}
		}
	}
	if blocking > 0 && policyOnly && slices.Contains([]string{"select", "amend", "confirm"}, cmd.Operation) {
		p.StopReason = "policy_unverified"
	}
	if cmd.AllowRepair != nil && !*cmd.AllowRepair && slices.Contains([]string{"select", "confirm"}, cmd.Operation) {
		p.StopReason = "repair_disabled"
	}
	if p.Validation.Status == "needs_input" {
		p.StopReason = "needs_input"
	}
	gapOnly := blocking > 0
	for _, issue := range p.Validation.Issues {
		if issue.Blocking && issue.Code != "hotel_price_unknown" && issue.Code != "total_budget_unknown" {
			gapOnly = false
		}
	}
	if gapOnly && slices.ContainsFunc(p.Tasks, func(t model.TripTask) bool { return t.Hotel != nil && t.Hotel.Candidate.PriceFen == nil }) {
		p.StopReason = "provider_capability_missing"
	}
	if slices.ContainsFunc(p.Validation.Issues, func(i model.TripIssue) bool {
		return i.Blocking && (strings.Contains(i.Code, "hard") || i.Code == "start_date_conflict" || i.Code == "end_date_conflict")
	}) {
		p.StopReason = "hard_constraint_conflict"
	}
}

func sourceUserWantsQuote(query string) bool {
	clauses := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return strings.ContainsRune("，。；！\n,;.!", r) })
	for _, clause := range clauses {
		negated := false
		for _, word := range []string{"不用", "不要", "无需", "不需要", "不关心", "不重要", "不核实", "not required", "no need"} {
			negated = negated || strings.Contains(clause, word)
		}
		if negated {
			continue
		}
		if strings.Contains(clause, "房价") {
			return true
		}
		for _, subject := range []string{"酒店", "住宿", "hotel"} {
			remaining := clause
			for {
				pos := strings.Index(remaining, subject)
				if pos < 0 {
					break
				}
				remaining = remaining[pos+len(subject):]
				rest := []rune(remaining)
				window := string(rest[:min(17, len(rest))])
				for _, price := range []string{"报价", "价格", "价钱", "费用", "price", "quote", "rate"} {
					if offset := strings.Index(window, price); offset >= 0 && len([]rune(window[:offset])) <= 12 {
						prefix := window[:offset]
						if !strings.Contains(prefix, "火车") && !strings.Contains(prefix, "车票") && !strings.Contains(prefix, "航班") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func validateSourcePatchScope(before model.TripTask, c model.TripTaskChange) error {
	if len(c.Components) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, part := range c.Components {
		if !slices.Contains([]string{"train", "hotel", "schedule", "content"}, part) {
			return invalid("invalid component scope")
		}
		allowed[part] = true
	}
	if allowed["schedule"] {
		allowed["train"] = true
		allowed["hotel"] = true
	}
	if c.Activities != nil && !reflect.DeepEqual(before.Activities, *c.Activities) && !allowed["content"] {
		return invalid("activity change exceeds component scope")
	}
	affected := map[string]bool{}
	if c.Origin != nil && *c.Origin != before.Origin {
		affected["train"] = true
	}
	if c.Destination != nil && *c.Destination != before.Destination {
		affected["train"] = true
		affected["hotel"] = true
	}
	if c.RequiresHotel != nil && *c.RequiresHotel != before.RequiresHotel {
		affected["hotel"] = true
	}
	for key, value := range c.Conditions {
		if semanticSourceEqual(before.Conditions[key], value) {
			continue
		}
		if key == "constraints" {
			old := filterValues(before.Conditions)
			next := filterValues(c.Conditions)
			for field := range mergeConditions(old, next) {
				if !semanticSourceEqual(old[field], next[field]) {
					sourceDomain(affected, field)
				}
			}
		} else {
			sourceDomain(affected, key)
		}
	}
	for key, level := range c.ConditionStrengths {
		if before.ConditionStrengths[key] != level {
			sourceDomain(affected, key)
		}
	}
	for domain := range affected {
		if !allowed[domain] {
			return invalid("change exceeds component scope: " + domain)
		}
	}
	return nil
}

func sourceDomain(out map[string]bool, key string) {
	if slices.Contains([]string{"departure_date", "arrival_date", "arrival_before", "passengers", "start_date", "end_date", "seat_class", "train_kind", "train_max_total_cny", "departure_time_after", "departure_time_before"}, key) {
		out["train"] = true
		return
	}
	if slices.Contains([]string{"check_in", "check_out", "nights", "guests", "hotel_keywords", "hotel_quote_required", "hotel_max_total_cny", "hotel_max_nightly_cny"}, key) {
		out["hotel"] = true
		return
	}
	out["train"] = true
	out["hotel"] = true
}
