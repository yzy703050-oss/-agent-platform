package travel

import (
	"encoding/json"
	"strings"
	"testing"

	pm "agent-platform/backend/internal/model"
)

func TestTravelSourceRelatedFacts(t *testing.T) {
	s := FlowState{Results: []pm.TravelToolResult{{Name: "query", Status: "ok", Data: json.RawMessage(`[{"domain":"train","query":{"origin":"上海","destination":"杭州"},"is_demo":true,"items":[{"train_number":"G1","price_cny":100.00,"seat_class":"二等座"}]},{"domain":"weather","query":{"city":"杭州"},"items":[{"description":"晴","temp_c":22}]}]`)}}}
	facts := sourceRelatedFacts(s.Results)
	for _, want := range []string{"上海", "杭州", "G1", "100.00", "22.0", "演示数据"} {
		if !strings.Contains(facts, want) {
			t.Fatal("independent facts lost", want, facts)
		}
	}
	answer, err := sourcePresentation("已经付款并预订成功，价格1元", s)
	if err != nil || strings.Contains(answer, "已经付款") || strings.Contains(answer, "1元") || !strings.Contains(answer, "100.00") {
		t.Fatal("unsupported booking claim escaped platform presentation guard", answer, err)
	}
	answer, err = sourcePresentation("G1 是可供参考的查询候选。", s)
	if err != nil || answer != "G1 是可供参考的查询候选。" {
		t.Fatal("ordinary source Summary was rewritten", answer, err)
	}
}
