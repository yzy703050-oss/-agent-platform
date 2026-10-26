package travel

import (
	"agent-platform/backend/internal/model"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// Route replacement is outside review authority regardless of leaf metadata.
// Only inspect a valid JSON envelope for an already-authorized plan here; the
// full normalizer/schema/business preflight still handles every accepted call.
func reviewRouteError(request ProfileRequest, call NativeCall) error {
	if request.Role != "verifier" || call.Name != "write_trip" {
		return nil
	}
	var args struct {
		Tripplan struct {
			Operation string          `json:"operation"`
			PlanID    string          `json:"trip_plan_id"`
			Route     json.RawMessage `json:"route_tasks"`
		} `json:"tripplan"`
	}
	if json.Unmarshal(call.Arguments, &args) != nil || args.Tripplan.Operation != "amend" || len(args.Tripplan.Route) == 0 || string(args.Tripplan.Route) == "null" || !slices.Contains(request.AllowedTripIDs, args.Tripplan.PlanID) {
		return nil
	}
	return reviewRepairError(request.Role, model.TripCommand{}, ruleError("REPLAN_REQUIRED", "route replacement requires Main", nil))
}

func reviewRepairError(role string, cmd model.TripCommand, err error) error {
	var rule *model.TravelRuleError
	if !errors.As(err, &rule) {
		return err
	}
	baseMessage := strings.SplitN(rule.Message, ":", 2)[0]
	hardCondition := rule.Code == "INVALID_ARGUMENTS" && slices.Contains([]string{"cannot soften hard task condition", "cannot soften hard global condition", "cannot soften inherited hard condition"}, baseMessage)
	if (role == "main" || role == "planning_summary") && hardCondition {
		return ruleError(rule.Code, rule.Message+"；保留已有硬条件强度，删去未授权的hard→soft标注；不为重排复制并重标整套元数据，只改实际受影响且已获准调整的字段。", rule.Details)
	}
	if (role == "main" || role == "planning_summary") && cmd.RouteTasks != nil && rule.Code == "INVALID_ARGUMENTS" {
		hint := ""
		switch rule.Message {
		case "route task identity unknown or repeated":
			hint = "route_tasks是完整新路线；每个旧task_id只能用一次，新增段省略task_id。每个段同时承载其目的地住宿活动，不另加同一路线的住宿段。"
		case "activity identity is unknown or repeated":
			hint = "route_tasks中活动id仅能在原所属task内复用；移入其他任务的活动省略id并从原任务删除，保留活动日期、时段、来源和强度。"
		case "route is not continuous":
			hint = "route_tasks相邻段前一destination必须等于后一origin；中转替换原直达段，不在新中转段后再次保留原直达路线。住宿与活动写在抵达目的地的那一段。"
		}
		if hint != "" {
			return ruleError(rule.Code, rule.Message+"；"+hint, rule.Details)
		}
	}
	if role != "verifier" {
		return err
	}
	replan := rule.Code == "REPLAN_REQUIRED" || rule.Code == "HARD_STAY_REQUIREMENT"
	if hardCondition {
		replan = true
	}
	if rule.Code == "INVALID_ARGUMENTS" && rule.Message == "activity identity is unknown or repeated" {
		for _, change := range cmd.Changes {
			if change.Activities == nil {
				continue
			}
			for _, a := range *change.Activities {
				if a.Strength == model.Hard {
					replan = true
				}
			}
		}
	}
	if replan {
		return ruleError("REPLAN_REQUIRED", "此修复不能放宽硬要求；下一步单独request_replan交Main，不重复当前amend。Main保留硬晚数、固定返程、硬预算与活动日期时段，优先调整已获准修改的软交通日期并同步住宿，不将硬条件改soft。", nil)
	}
	return err
}
