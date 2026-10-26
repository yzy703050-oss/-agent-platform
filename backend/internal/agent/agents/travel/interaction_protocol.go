package travel

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const interactionRegenerationInstruction = "上一响应违反了不向用户索取信息或确认的交互规则，整次响应未接受且工具未执行。不要再问出发地、用途、返程、住宿、人数、席别或预算。优先本轮明确要求及已注入有效记忆，仍缺出发地就使用已授权的重庆proposal/soft默认；缺其他条件按planning_defaults自行安排。普通规划一次生成含去程、返程及必要住宿活动的完整tripplan和查询地图，不停在缺起点/等待补充；明确单程或不住宿优先。Main仅首次规划与真实政策例外，恢复由规划总结接手；已完成审查提交绑定回复，整体重排由规划总结直接amend当前行程，不调用request_replan或Main。"

var historyOperation = regexp.MustCompile(`(?:修改|调整|更改|取消|删除|恢复|重排|沿用|继续|复制|查看|查阅|查询|找出|对比|比较|列出).{0,24}(?:行程|计划|安排|记录|会话|偏好)|(?:show|view|change|modify|update|cancel|compare|reuse).{0,30}(?:trip|plan|itinerary|history|preference)`)

// Restrict only clearly new planning requests. An explicit historical reference
// or edit keeps the original history tools; no extra model classifies intent.
func freshPlanningRequest(query string) bool {
	query = strings.ToLower(query)
	planning := strings.Contains(query, "规划") || strings.Contains(query, "安排") || strings.Contains(query, "计划") || strings.Contains(query, "plan ") || strings.Contains(query, "arrange ")
	if !planning {
		return false
	}
	for _, clause := range strings.FieldsFunc(query, func(r rune) bool { return strings.ContainsRune("，。；,;.\n", r) }) {
		if strings.Contains(clause, "不要") || strings.Contains(clause, "不用") || strings.Contains(clause, "无需") || strings.Contains(clause, "不必") || strings.Contains(clause, "don't") || strings.Contains(clause, "do not") {
			continue
		}
		if historyOperation.MatchString(clause) {
			return false
		}
		for _, word := range []string{"历史行程", "历史记录", "历史会话", "出行历史", "差旅历史", "上次", "之前", "过去的行程", "上一", "旧行程", "已有", "已保存", "原计划", "刚才", "刚刚", "history", "previous", "last trip", "saved", "existing"} {
			if strings.Contains(clause, word) {
				return false
			}
		}
	}
	return true
}

// Detect explicit requests for user input, not every question mark or a
// statement that a supplier/policy fact is unknown. Rejecting a whole response
// allows the model to correct it without deleting prose or executing its tools.
var userInputRequest = regexp.MustCompile(`(?:我先确认|先确认一下)(?:一下)?[^。！？\n]{0,10}(?:您|你)(?:的)?(?:出发|人数|用途|预算|行程)|补充(?:一下|您的|你的)?(?:出发地|起点|人数|预算)[^。！？\n]{0,10}(?:后|再)|(?:请|麻烦|劳烦)(?:您|你)?[^。！？\n]{0,24}(?:补充|提供|告知|确认|告诉|回答)|(?:我|还|仍)?需要(?:先)?(?:确认|了解|知道)[^。！？\n]{0,20}|告诉我|告知我|(?:您|你)(?:先|再|还需|需要|需|要|可以|能否|是否能|可否)?(?:补充|提供|确认|告知)|(?:您|你)?(?:是)?从(?:哪|什么)[^。！？\n]{0,18}出发|(?:出发城市|出发地|返程日期|几人|住几晚)[^。！？\n]{0,30}[?？]`)

func asksForUserInput(text string) bool {
	for _, at := range userInputRequest.FindAllStringIndex(text, -1) {
		prefix := strings.TrimSpace(text[:at[0]])
		if strings.HasSuffix(prefix, "不") || strings.HasSuffix(prefix, "无需") || strings.HasSuffix(prefix, "不需要") || strings.HasSuffix(prefix, "不用") || strings.HasSuffix(prefix, "不必") {
			continue
		}
		return true
	}
	return false
}

func sourceInteractionFailure(response *schema.Message) string {
	if asksForUserInput(response.Content) {
		return "user_input_requested"
	}
	for _, call := range response.ToolCalls {
		if call.Function.Name != "write_trip" {
			continue
		}
		var args struct {
			Reply *struct {
				Explanation string `json:"explanation"`
			} `json:"reply"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Reply != nil && asksForUserInput(args.Reply.Explanation) {
			return "user_input_requested"
		}
	}
	return ""
}
