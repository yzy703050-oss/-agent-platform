// Package travelplan separates structural plan changes from provider facts and
// model planning decisions. It does not rank or filter candidates by hard/soft rules.
package travelplan

import (
	"encoding/json"
	"fmt"
	"slices"

	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeprotocol"
)

func invalid(path, message string) error {
	return fmt.Errorf("%w at %s: %s", model.ErrInvalidInput, path, message)
}
func checked(value any, kind string, out any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return runtimeprotocol.DecodeStrict(raw, kind, out)
}
func canonical(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	b, err := runtimeprotocol.CanonicalJSON(raw)
	return string(b), err
}

// ValidateDefinition enforces the formal schema and graph, not whether a plan
// satisfies budgets, preferred times or policy. Those decisions belong to the model.
func ValidateDefinition(d v1.PlanDefinition) error {
	var checkedDefinition v1.PlanDefinition
	if err := checked(d, "PlanDefinition", &checkedDefinition); err != nil {
		return err
	}
	d = checkedDefinition
	stops := map[string]v1.Stop{}
	legs := map[string]v1.Leg{}
	tasks := map[string]v1.TaskDefinition{}
	requirements := map[string]v1.Requirement{}
	items := map[string]v1.QueryItem{}
	for _, stop := range d.Stops {
		if _, ok := stops[stop.StopID]; ok {
			return invalid("stops", "duplicate stop ID")
		}
		stops[stop.StopID] = stop
		if stop.ArrivalDate.Present && !stop.ArrivalDate.Null && stop.DepartureDate.Present && !stop.DepartureDate.Null && stop.ArrivalDate.Value > stop.DepartureDate.Value {
			return invalid("stops", "reversed stop dates")
		}
	}
	for _, leg := range d.Legs {
		if _, ok := legs[leg.LegID]; ok {
			return invalid("legs", "duplicate leg ID")
		}
		legs[leg.LegID] = leg
		if _, ok := stops[leg.FromStopID]; !ok {
			return invalid("legs.from_stop_id", "missing stop")
		}
		if _, ok := stops[leg.ToStopID]; !ok {
			return invalid("legs.to_stop_id", "missing stop")
		}
		if leg.FromStopID == leg.ToStopID {
			return invalid("legs", "endpoints need distinct stop IDs")
		}
	}
	for _, task := range d.Tasks {
		if _, ok := tasks[task.TaskID]; ok {
			return invalid("tasks", "duplicate task ID")
		}
		tasks[task.TaskID] = task
		for _, item := range task.Items {
			if _, ok := items[item.ItemID]; ok {
				return invalid("tasks.items", "duplicate item ID")
			}
			items[item.ItemID] = item
		}
	}
	for _, r := range d.Requirements {
		if _, ok := requirements[r.RequirementID]; ok {
			return invalid("requirements", "duplicate requirement ID")
		}
		requirements[r.RequirementID] = r
		if len(r.TaskIDs) > 0 != (r.Scope == "tasks") {
			return invalid("requirements.scope", "scope and affected tasks disagree")
		}
		if !unique(r.TaskIDs) {
			return invalid("requirements.task_ids", "duplicate task")
		}
		for _, id := range r.TaskIDs {
			if _, ok := tasks[id]; !ok {
				return invalid("requirements.task_ids", "missing task")
			}
		}
		kind := map[string]string{"user": "message", "policy": "policy", "memory": "memory"}[r.Origin]
		matches := false
		for _, source := range r.Sources {
			if source.Kind == kind {
				matches = true
			}
		}
		if !matches {
			return invalid("requirements.sources", "origin and source kind disagree")
		}
	}
	for _, task := range d.Tasks {
		switch task.Target.Kind {
		case "stop":
			if _, ok := stops[task.Target.ID.Value]; !ok {
				return invalid("tasks.target", "missing stop")
			}
		case "leg":
			if _, ok := legs[task.Target.ID.Value]; !ok {
				return invalid("tasks.target", "missing leg")
			}
		}
		if !unique(task.RequirementIDs) {
			return invalid("tasks.requirement_ids", "duplicate requirement")
		}
		expected := []string{}
		for _, r := range d.Requirements {
			if slices.Contains(r.TaskIDs, task.TaskID) {
				expected = append(expected, r.RequirementID)
			}
		}
		if !sameSet(expected, task.RequirementIDs) {
			return invalid("tasks.requirement_ids", "requirement references disagree")
		}
		for _, id := range task.RequirementIDs {
			if _, ok := requirements[id]; !ok {
				return invalid("tasks.requirement_ids", "missing requirement")
			}
		}
		for _, item := range task.Items {
			present := false
			switch item.Domain {
			case "hotel":
				present = task.Parameters.Hotel.Present && !task.Parameters.Hotel.Null
			case "train":
				present = task.Parameters.Train.Present && !task.Parameters.Train.Null
			case "weather":
				present = task.Parameters.Weather.Present && !task.Parameters.Weather.Null
			case "web":
				present = task.Parameters.Web.Present && !task.Parameters.Web.Null
			}
			if !present {
				return invalid("tasks.parameters", "query domain parameters object missing")
			}
		}
		h := task.Parameters.Hotel.Value
		if h.CheckIn.Present && !h.CheckIn.Null && h.CheckOut.Present && !h.CheckOut.Null && h.CheckIn.Value >= h.CheckOut.Value {
			return invalid("tasks.parameters.hotel", "checkout must follow checkin")
		}
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		item, ok := items[id]
		if !ok {
			return invalid("items.depends_on", "missing dependency")
		}
		if active[id] {
			return invalid("items.depends_on", "cyclic dependency")
		}
		if visited[id] {
			return nil
		}
		if !unique(item.DependsOn) {
			return invalid("items.depends_on", "duplicate dependency")
		}
		active[id] = true
		for _, dependency := range item.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(active, id)
		visited[id] = true
		return nil
	}
	for id := range items {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func InitialSnapshot(id, thread string, definition v1.PlanDefinition) (v1.PlanSnapshot, error) {
	var d v1.PlanDefinition
	if err := checked(definition, "PlanDefinition", &d); err != nil {
		return v1.PlanSnapshot{}, err
	}
	snapshot := v1.PlanSnapshot{SchemaVersion: 1, PlanID: id, TravelThreadID: thread, Revision: 1, Definition: d, TaskVersions: []v1.TaskVersion{}, Selections: []v1.Selection{}}
	for _, task := range d.Tasks {
		snapshot.TaskVersions = append(snapshot.TaskVersions, v1.TaskVersion{TaskID: task.TaskID, TaskRevision: 1})
	}
	return snapshot, validateSnapshot(snapshot)
}

func unique(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func sameSet(a, b []string) bool {
	if len(a) != len(b) || !unique(a) || !unique(b) {
		return false
	}
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}
	return true
}

func effectiveTask(d v1.PlanDefinition, task v1.TaskDefinition) (string, error) {
	requirements := []v1.Requirement{}
	for _, r := range d.Requirements {
		if r.Scope == "plan" || slices.Contains(r.TaskIDs, task.TaskID) {
			requirements = append(requirements, r)
		}
	}
	stops := map[string]v1.Stop{}
	for _, stop := range d.Stops {
		stops[stop.StopID] = stop
	}
	route := map[string]any{}
	if task.Target.Kind == "stop" {
		route["stop"] = stops[task.Target.ID.Value]
	}
	legs := []v1.Leg{}
	for _, leg := range d.Legs {
		if task.Target.Kind == "leg" && leg.LegID == task.Target.ID.Value || task.Target.Kind == "stop" && (leg.FromStopID == task.Target.ID.Value || leg.ToStopID == task.Target.ID.Value) {
			legs = append(legs, leg)
			route[leg.FromStopID] = stops[leg.FromStopID]
			route[leg.ToStopID] = stops[leg.ToStopID]
		}
	}
	return canonical(map[string]any{"task": task, "requirements": requirements, "route": route, "legs": legs, "timezone": d.Timezone})
}

func validateSnapshot(snapshot v1.PlanSnapshot) error {
	var out v1.PlanSnapshot
	if err := checked(snapshot, "PlanSnapshot", &out); err != nil {
		return err
	}
	if err := ValidateDefinition(snapshot.Definition); err != nil {
		return err
	}
	versions := map[string]int64{}
	for _, v := range snapshot.TaskVersions {
		if _, ok := versions[v.TaskID]; ok {
			return invalid("task_versions", "duplicate task")
		}
		versions[v.TaskID] = v.TaskRevision
	}
	if len(versions) != len(snapshot.Definition.Tasks) {
		return invalid("task_versions", "task coverage mismatch")
	}
	tasks := map[string]v1.TaskDefinition{}
	for _, task := range snapshot.Definition.Tasks {
		tasks[task.TaskID] = task
		if versions[task.TaskID] < 1 {
			return invalid("task_versions", "missing task version")
		}
	}
	selectionIDs := map[string]bool{}
	for _, selection := range snapshot.Selections {
		if selectionIDs[selection.SelectionID] {
			return invalid("selections", "duplicate selection ID")
		}
		selectionIDs[selection.SelectionID] = true
		task, ok := tasks[selection.TaskID]
		if !ok || versions[selection.TaskID] != selection.TaskRevision {
			return invalid("selections", "missing task or stale version")
		}
		found := false
		for _, item := range task.Items {
			if item.ItemID == selection.ItemID {
				found = true
			}
		}
		if !found {
			return invalid("selections.item_id", "missing item")
		}
	}
	return nil
}
