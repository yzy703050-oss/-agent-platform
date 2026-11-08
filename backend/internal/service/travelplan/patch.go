package travelplan

import (
	"encoding/json"
	"slices"
	"strings"

	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
)

type Change struct {
	Snapshot                             v1.PlanSnapshot
	Changed                              bool
	InvalidatedTaskIDs, RefreshedItemIDs []string
}

// ProposePatch applies the entire operation list to a private copy, then checks
// the final graph. A proposal alone never changes the current plan or proves
// a selected evidence reference exists; the commit boundary checks provenance.
func ProposePatch(base v1.PlanSnapshot, patch v1.PlanPatch) (Change, error) {
	var result Change
	if err := validateSnapshot(base); err != nil {
		return result, err
	}
	var decoded v1.PlanPatch
	if err := checked(patch, "PlanPatch", &decoded); err != nil {
		return result, err
	}
	patch = decoded
	if patch.PlanID != base.PlanID || patch.ExpectedRevision != base.Revision {
		return result, model.ErrConflict
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return result, err
	}
	var copy map[string]any
	if err = json.Unmarshal(raw, &copy); err != nil {
		return result, err
	}
	definition := copy["definition"].(map[string]any)
	refresh := map[string]bool{}
	explicitSelections := map[string]bool{}
	for _, operation := range patch.Operations {
		switch operation.Op {
		case "update_plan":
			if err = applyChanges(definition, operation.Op, operation.Changes.Value); err != nil {
				return result, err
			}
		case "add_stop", "add_leg", "add_task", "add_requirement", "update_stop", "update_leg", "update_task", "update_requirement", "remove_stop", "remove_leg", "remove_task", "remove_requirement":
			parts := strings.SplitN(operation.Op, "_", 2)
			kind := parts[1]
			collection := kind + "s"
			if kind == "requirement" {
				collection = "requirements"
			}
			ids := map[string]string{"stop": operation.StopID.Value, "leg": operation.LegID.Value, "task": operation.TaskID.Value, "requirement": operation.RequirementID.Value}
			id := ids[kind]
			rows := definition[collection].([]any)
			index := -1
			var value map[string]any
			if parts[0] == "add" {
				if err = json.Unmarshal(operation.Value.Value, &value); err != nil {
					return result, err
				}
				id, _ = value[kind+"_id"].(string)
			}
			for i, row := range rows {
				if row.(map[string]any)[kind+"_id"] == id {
					index = i
				}
			}
			switch parts[0] {
			case "add":
				if index >= 0 {
					return result, invalid(collection, "ID already exists")
				}
				rows = append(rows, value)
			case "remove":
				if index < 0 {
					return result, invalid(collection, "ID not found")
				}
				rows = append(rows[:index], rows[index+1:]...)
			case "update":
				if index < 0 {
					return result, invalid(collection, "ID not found")
				}
				if err = applyChanges(rows[index].(map[string]any), operation.Op, operation.Changes.Value); err != nil {
					return result, err
				}
			}
			definition[collection] = rows
		case "reorder_stops", "reorder_tasks":
			collection, key, ids := "stops", "stop_id", operation.StopIDs.Value
			if operation.Op == "reorder_tasks" {
				collection, key, ids = "tasks", "task_id", operation.TaskIDs.Value
			}
			rows := definition[collection].([]any)
			if len(ids) != len(rows) || !unique(ids) {
				return result, invalid(collection, "reorder must contain every ID once")
			}
			ordered := []any{}
			for _, id := range ids {
				found := false
				for _, row := range rows {
					if row.(map[string]any)[key] == id {
						ordered = append(ordered, row)
						found = true
					}
				}
				if !found {
					return result, invalid(collection, "unknown reordered ID")
				}
			}
			definition[collection] = ordered
		case "set_selection":
			var value map[string]any
			if err = json.Unmarshal(operation.Value.Value, &value); err != nil {
				return result, err
			}
			id := value["selection_id"].(string)
			explicitSelections[id] = true
			rows := copy["selections"].([]any)
			replaced := false
			for i, row := range rows {
				if row.(map[string]any)["selection_id"] == id {
					rows[i] = value
					replaced = true
				}
			}
			if !replaced {
				rows = append(rows, value)
			}
			copy["selections"] = rows
		case "clear_selection":
			rows := copy["selections"].([]any)
			found := false
			for i, row := range rows {
				if row.(map[string]any)["selection_id"] == operation.SelectionID.Value {
					rows = append(rows[:i], rows[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				return result, invalid("selections", "selection ID not found")
			}
			copy["selections"] = rows
			delete(explicitSelections, operation.SelectionID.Value)
		case "refresh_queries":
			if !unique(operation.ItemIDs.Value) {
				return result, invalid("refresh_queries", "duplicate item")
			}
			for _, id := range operation.ItemIDs.Value {
				if refresh[id] {
					return result, invalid("refresh_queries", "item refreshed more than once")
				}
				refresh[id] = true
				result.RefreshedItemIDs = append(result.RefreshedItemIDs, id)
			}
		default:
			return result, invalid("operations", "unsupported operation")
		}
	}
	raw, err = json.Marshal(copy)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result.Snapshot); err != nil {
		return result, err
	}
	if err = ValidateDefinition(result.Snapshot.Definition); err != nil {
		return result, err
	}
	// Check refresh IDs against the completed graph, including same-patch edits.
	for _, operation := range patch.Operations {
		if operation.Op != "refresh_queries" {
			continue
		}
		taskFound := false
		for _, task := range result.Snapshot.Definition.Tasks {
			if task.TaskID != operation.TaskID.Value {
				continue
			}
			taskFound = true
			for _, id := range operation.ItemIDs.Value {
				found := false
				for _, item := range task.Items {
					if item.ItemID == id {
						found = true
					}
				}
				if !found {
					return result, invalid("refresh_queries.item_ids", "item not in task")
				}
			}
		}
		if !taskFound {
			return result, invalid("refresh_queries.task_id", "task not found")
		}
	}
	prior := map[string]v1.TaskDefinition{}
	versions := map[string]int64{}
	for _, task := range base.Definition.Tasks {
		prior[task.TaskID] = task
	}
	for _, version := range base.TaskVersions {
		versions[version.TaskID] = version.TaskRevision
	}
	result.Snapshot.TaskVersions = []v1.TaskVersion{}
	invalidated := map[string]bool{}
	for _, task := range result.Snapshot.Definition.Tasks {
		version := int64(1)
		if old, ok := prior[task.TaskID]; ok {
			version = versions[task.TaskID]
			a, e := effectiveTask(base.Definition, old)
			if e != nil {
				return result, e
			}
			b, e := effectiveTask(result.Snapshot.Definition, task)
			if e != nil {
				return result, e
			}
			if a != b {
				version++
				invalidated[task.TaskID] = true
			}
			delete(prior, task.TaskID)
		}
		result.Snapshot.TaskVersions = append(result.Snapshot.TaskVersions, v1.TaskVersion{TaskID: task.TaskID, TaskRevision: version})
	}
	for id := range prior {
		invalidated[id] = true
	}
	for _, task := range base.Definition.Tasks {
		if invalidated[task.TaskID] {
			result.InvalidatedTaskIDs = append(result.InvalidatedTaskIDs, task.TaskID)
		}
	}
	selections := []v1.Selection{}
	for _, selection := range result.Snapshot.Selections {
		if invalidated[selection.TaskID] && !explicitSelections[selection.SelectionID] {
			continue
		}
		selections = append(selections, selection)
	}
	result.Snapshot.Selections = selections
	a, e := canonical(base)
	if e != nil {
		return result, e
	}
	b, e := canonical(result.Snapshot)
	if e != nil {
		return result, e
	}
	result.Changed = a != b
	if result.Changed {
		result.Snapshot.Revision = base.Revision + 1
	}
	if err = validateSnapshot(result.Snapshot); err != nil {
		return Change{}, err
	}
	return result, nil
}

var writable = map[string][]string{
	"update_plan": {"title", "goal", "purpose", "timezone"}, "update_stop": {"city", "arrival_date", "departure_date"}, "update_leg": {"from_stop_id", "to_stop_id", "departure_date"}, "update_task": {"title", "target", "parameters", "requirement_ids", "ranking", "items"}, "update_requirement": {"text", "origin", "scope", "task_ids", "sources"},
}
var parameterFields = map[string][]string{"hotel": {"city", "check_in", "check_out", "guests", "keywords"}, "train": {"origin", "destination", "departure_date", "passengers", "keywords"}, "web": {"query", "time_range"}, "weather": {"city", "date"}}

func applyChanges(object map[string]any, op string, changes v1.FieldChanges) error {
	if changes.Set.Null || changes.Unset.Null {
		return invalid("changes", "null changes are not supported")
	}
	paths := []string{}
	for key := range changes.Set.Value {
		paths = append(paths, key)
	}
	paths = append(paths, changes.Unset.Value...)
	if len(paths) == 0 || !unique(paths) {
		return invalid("changes", "empty or overlapping changes")
	}
	for _, path := range paths {
		parts := strings.Split(path, ".")
		if !slices.Contains(writable[op], parts[0]) {
			return invalid(path, "protected or unknown path")
		}
		if len(parts) > 1 {
			if op != "update_task" || parts[0] != "parameters" || len(parts) > 3 {
				return invalid(path, "arbitrary nested path")
			}
			fields, ok := parameterFields[parts[1]]
			if !ok || len(parts) == 3 && !slices.Contains(fields, parts[2]) {
				return invalid(path, "unknown parameter field")
			}
		}
		for _, other := range paths {
			if path != other && strings.HasPrefix(other, path+".") {
				return invalid(path, "ancestor/descendant overlap")
			}
		}
	}
	for _, path := range paths {
		parts := strings.Split(path, ".")
		parent := object
		_, setting := changes.Set.Value[path]
		for _, part := range parts[:len(parts)-1] {
			child, ok := parent[part].(map[string]any)
			if !ok {
				if _, exists := parent[part]; exists && parent[part] != nil {
					return invalid(path, "parent is not an object")
				}
				if !setting {
					parent = nil
					break
				}
				child = map[string]any{}
				parent[part] = child
			}
			parent = child
		}
		if parent == nil {
			continue
		}
		key := parts[len(parts)-1]
		if setting {
			var value any
			if err := json.Unmarshal(changes.Set.Value[path], &value); err != nil {
				return err
			}
			parent[key] = value
		} else {
			delete(parent, key)
		}
	}
	return nil
}
