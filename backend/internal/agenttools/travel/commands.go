package travel

import (
	"encoding/json"
	"slices"

	"agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
)

func businessCommand(args NativeTripArguments, decision model.TripDecisionContext) (model.TripCommand, error) {
	p := args.Tripplan
	cmd := model.TripCommand{Operation: p.Operation, ConfirmedConditions: model.TripConditions(p.ConfirmedConditions), FieldSources: p.FieldSources, ConditionStrengths: strengths(p.ConditionStrengths), RoundTrip: p.DefaultRoundTrip, AllowRepair: p.AllowRepair, RepairIssueCodes: p.RepairIssueCodes, Context: &decision}
	if p.TripPlanID != nil {
		cmd.PlanID = *p.TripPlanID
	}
	if p.ExpectedRevision != nil {
		cmd.ExpectedRevision = *p.ExpectedRevision
	}
	if p.Reason != nil {
		cmd.Reason = *p.Reason
	}
	for _, task := range p.Tasks {
		if p.Operation == "propose" {
			row := model.TripTask{Conditions: model.TripConditions(task.Conditions), FieldSources: task.FieldSources, ConditionStrengths: strengths(task.ConditionStrengths)}
			if task.Origin != nil {
				row.Origin = *task.Origin
			}
			if task.Destination != nil {
				row.Destination = *task.Destination
			}
			if task.Purpose != nil {
				row.Purpose = *task.Purpose
			}
			if task.RequiresHotel != nil {
				row.RequiresHotel = *task.RequiresHotel
			}
			if task.Content != nil {
				row.Activities = *task.Content
			}
			if task.Items != nil {
				row.Items = businessItems(*task.Items)
			}
			cmd.Tasks = append(cmd.Tasks, row)
		} else {
			if task.TaskID == nil || *task.TaskID == "" {
				return cmd, ruleError("INVALID_ARGUMENTS", "existing task needs task_id", nil)
			}
			change := businessPatch(task)
			if p.Operation == "amend" {
				cmd.Changes = append(cmd.Changes, change)
			}
			for domain, ref := range task.SelectionRefs {
				if len(task.Components) > 0 && !slices.Contains(task.Components, domain) && !slices.Contains(task.Components, "schedule") {
					return cmd, ruleError("INVALID_ARGUMENTS", "selection exceeds component scope", nil)
				}
				cmd.Selections = append(cmd.Selections, model.TripSelection{TaskID: *task.TaskID, Domain: domain, Ref: ref, ExpectedRevision: task.ExpectedRevision})
			}
		}
	}
	if p.RouteTasks != nil {
		rows := []model.TripTaskChange{}
		for _, task := range *p.RouteTasks {
			rows = append(rows, businessPatch(task))
			for domain, ref := range task.SelectionRefs {
				if task.TaskID == nil {
					return cmd, ruleError("INVALID_ARGUMENTS", "route selection needs an existing task_id", nil)
				}
				cmd.Selections = append(cmd.Selections, model.TripSelection{TaskID: *task.TaskID, Domain: domain, Ref: ref, ExpectedRevision: task.ExpectedRevision})
			}
		}
		cmd.RouteTasks = &rows
	}
	return cmd, nil
}
func businessPatch(task NativeTask) model.TripTaskChange {
	change := model.TripTaskChange{ExpectedRevision: task.ExpectedRevision, Origin: task.Origin, Destination: task.Destination, Purpose: task.Purpose, RequiresHotel: task.RequiresHotel, Conditions: model.TripConditions(task.Conditions), FieldSources: task.FieldSources, ConditionStrengths: strengths(task.ConditionStrengths), Activities: task.Content, Components: task.Components}
	if task.TaskID != nil {
		change.TaskID = *task.TaskID
	}
	if task.Items != nil {
		items := businessItems(*task.Items)
		change.Items = &items
	}
	return change
}
func strengths(values map[string]string) map[string]model.ConstraintStrength {
	out := map[string]model.ConstraintStrength{}
	for key, value := range values {
		out[key] = model.ConstraintStrength(value)
	}
	return out
}
func businessItems(values []NativeQueryItem) []model.TripQueryItem {
	out := make([]model.TripQueryItem, 0, len(values))
	for _, value := range values {
		item := model.TripQueryItem{Domain: value.Domain, Parameters: model.TripConditions(value.Parameters), Constraints: model.TripConditions(value.Constraints), FieldSources: value.FieldSources, ConditionStrengths: strengths(value.ConditionStrengths), DependsOn: value.DependsOn, Refresh: value.Refresh}
		for _, ranking := range value.Ranking {
			item.Ranking = append(item.Ranking, model.TripRanking{Field: ranking.Field, Direction: ranking.Direction, Preferred: ranking.Preferred})
		}
		out = append(out, item)
	}
	return out
}
func businessQuery(value NativeQueryItem) (model.TravelQuery, error) {
	return tripsvc.QueryFromItem(businessItems([]NativeQueryItem{value})[0])
}
func equalRetryQuery(a, b model.TravelQuery) bool {
	if a.OriginalParameters != nil && b.OriginalParameters != nil {
		x, _ := json.Marshal(a.OriginalParameters)
		y, _ := json.Marshal(b.OriginalParameters)
		return a.Domain == b.Domain && string(x) == string(y)
	}
	// Scope equality is about original query parameters, not cache projection.
	a.Constraints = model.TripConstraints{}
	b.Constraints = model.TripConstraints{}
	a.Ranking = nil
	b.Ranking = nil
	a.Refresh = false
	b.Refresh = false
	a.PlanID = ""
	b.PlanID = ""
	a.PlanRevision = 0
	b.PlanRevision = 0
	a.TaskID = ""
	b.TaskID = ""
	a.TaskRevision = 0
	b.TaskRevision = 0
	a.SourceVersion = 0
	b.SourceVersion = 0
	a.OriginalParameters = nil
	b.OriginalParameters = nil
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
