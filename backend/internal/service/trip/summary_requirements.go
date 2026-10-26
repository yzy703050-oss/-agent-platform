package trip

import (
	"encoding/json"
	"slices"

	"agent-platform/backend/internal/model"
)

// Route authority is not authority to alter the user's hard requirements.
// Activity identities may change when correcting ownership, so compare their
// hard semantic values across the complete route rather than only by task ID.
func protectSummaryRequirements(before, after model.TripPlan) error {
	fail := func(field string) error {
		return &model.TravelRuleError{Code: "SUMMARY_HARD_REQUIREMENT", Message: "规划总结不能更改或删除用户硬要求：" + field + "；保留原值，仅调整已授权软条件并直接修复当前行程，不交回Main。"}
	}
	leaves := func(c model.TripConditions) model.TripConditions { return mergeConditions(c, filterValues(c)) }
	for key, value := range leaves(before.ConfirmedConditions) {
		if key == "constraints" || missingValue(value) || before.ConditionStrengths[key] == model.Soft {
			continue
		}
		if !semanticSourceEqual(value, conditionLeaf(after.ConfirmedConditions, key)) || after.ConditionStrengths[key] == model.Soft {
			return fail("confirmed_conditions." + key)
		}
	}
	if len(before.Tasks) > 0 && (len(after.Tasks) == 0 || before.Tasks[0].Origin != after.Tasks[0].Origin || before.Tasks[len(before.Tasks)-1].Destination != after.Tasks[len(after.Tasks)-1].Destination) {
		return fail("route endpoints")
	}
	for _, old := range before.Tasks {
		index := slices.IndexFunc(after.Tasks, func(task model.TripTask) bool { return task.ID == old.ID })
		if old.Purpose != "transit" && (index < 0 || after.Tasks[index].Destination != old.Destination) {
			return &model.TravelRuleError{Code: "SUMMARY_HARD_REQUIREMENT", Message: "保留目的地" + old.Destination + "的原task_id=" + old.ID + "，不得把它用于新增中转段；新增中转段省略task_id，原目的地任务只修改起点和已授权软日期，不交回Main。"}
		}
		for key, value := range leaves(old.Conditions) {
			source := old.FieldSources[key]
			if source == "" {
				source = before.FieldSources[key]
			}
			if key == "constraints" || missingValue(value) || !sourceHard(before, old, key) || !slices.Contains([]string{"user", "context"}, source) {
				continue
			}
			if index < 0 || !semanticSourceEqual(value, conditionLeaf(after.Tasks[index].Conditions, key)) || !sourceHard(after, after.Tasks[index], key) {
				return fail("tasks." + old.ID + ".conditions." + key)
			}
		}
		if old.Purpose != "transit" && !slices.ContainsFunc(after.Tasks, func(task model.TripTask) bool { return task.Destination == old.Destination }) {
			return fail("destination:" + old.Destination)
		}
	}
	type hardActivity struct {
		Purpose, Date, Time string
		Strength            model.ConstraintStrength
	}
	activityCounts := func(p model.TripPlan) map[string]int {
		counts := map[string]int{}
		for _, task := range p.Tasks {
			for _, a := range task.Activities {
				if a.Strength == model.Hard {
					key, _ := json.Marshal(hardActivity{a.Purpose, a.Date, a.Time, a.Strength})
					counts[string(key)]++
				}
			}
		}
		return counts
	}
	newActivities := activityCounts(after)
	for activity, count := range activityCounts(before) {
		if newActivities[activity] < count {
			return fail("hard activities")
		}
	}
	return nil
}
