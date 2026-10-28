package travel

import (
	_ "embed"
	"strings"

	"github.com/cloudwego/eino/schema"
)

var (
	//go:embed schemas/runtime_write_trip_help.txt
	runtimeWriteTripHelp string
	//go:embed schemas/runtime_query_policy_help.txt
	runtimeQueryPolicyHelp string
)

const (
	runtimeWriteScope    = "按当前schema允许的操作创建、选择或原子修改行程。"
	runtimeSummaryScope  = "仅amend/select当前授权计划，直接修复整体路线并绑定总结；不交回Main。"
	runtimeRecoveryScope = "首次Main未保存成功时，仅恢复本请求的propose；已有计划不得创建替代计划。"
)

// Include both resource text and description-composition decisions in the
// compatibility identity; source profiles remain independently fingerprinted.
func RuntimeToolInstructionMaterial() string {
	return strings.Join([]string{"tool-instructions-v1", TripArgumentHelp, runtimeWriteTripHelp, runtimeQueryPolicyHelp,
		runtimeWriteScope, runtimeSummaryScope, runtimeRecoveryScope}, "\x00")
}

// SourceProfile remains the frozen migration contract. Descriptions are changed
// only on fresh runtime copies, after the existing permission filters.
func withRuntimeToolInstructions(profile string, tools []*schema.ToolInfo) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(tools))
	for _, info := range tools {
		copy := *info
		switch info.Name {
		case "write_trip":
			scope := runtimeWriteScope
			switch profile {
			case "planning_summary":
				scope = runtimeSummaryScope
			case "planning_recovery":
				scope = runtimeRecoveryScope
			case "review", "review_retry":
				scope = strings.TrimSuffix(info.Desc, TripArgumentHelp)
			}
			copy.Desc = scope + TripArgumentHelp + "\n" + runtimeWriteTripHelp
		case "query_policy":
			copy.Desc = info.Desc + "\n" + runtimeQueryPolicyHelp
		}
		out = append(out, &copy)
	}
	return out
}
