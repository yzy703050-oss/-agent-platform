package travel

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// This runtime extension never mutates the frozen source profiles.
func planningSummaryProfile(recovery bool) ([]*schema.ToolInfo, error) {
	base, err := SourceProfile("main")
	if err != nil {
		return nil, err
	}
	out := []*schema.ToolInfo{}
	for _, info := range base {
		if !slices.Contains([]string{"write_trip", "query", "query_policy", "read"}, info.Name) {
			continue
		}
		if info.Name == "write_trip" {
			params, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, err
			}
			data, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var doc map[string]any
			if err = json.Unmarshal(data, &doc); err != nil {
				return nil, err
			}
			properties, ok := doc["properties"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("summary profile properties missing")
			}
			plan, ok := properties["tripplan"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("summary profile tripplan missing")
			}
			branches, ok := plan["oneOf"].([]any)
			if !ok {
				return nil, fmt.Errorf("summary profile operations missing")
			}
			keep := []any{}
			for _, branch := range branches {
				row, _ := branch.(map[string]any)
				ref, _ := row["$ref"].(string)
				if recovery && ref == "#/$defs/CreatePlan" || !recovery && slices.Contains([]string{"#/$defs/SelectPlan", "#/$defs/AmendPlan"}, ref) {
					keep = append(keep, branch)
				}
			}
			plan["oneOf"] = keep
			data, err = json.Marshal(doc)
			if err != nil {
				return nil, err
			}
			var parsed jsonschema.Schema
			if err = json.Unmarshal(data, &parsed); err != nil {
				return nil, err
			}
			info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&parsed)
			info.Desc = "规划总结：直接修复当前请求的路线、日期、活动与查询地图并选择和总结；保留硬要求和有效成果，不调用Main或request_replan。已有计划只能amend/select，首次创建失败才能恢复propose。"
		}
		out = append(out, info)
	}
	return out, nil
}
