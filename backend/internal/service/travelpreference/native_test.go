package travelpreference

import (
	"testing"

	"agent-platform/backend/internal/model"
)

func TestTravelSourceMemory(t *testing.T) {
	text, id := func(v string) *string { return &v }, func(v string) *string { return &v }
	rows := []model.TravelPreference{{Key: "memory_explicit", Value: "优先靠窗", Revision: 1, Origin: "explicit"}, {Key: "memory_retracted", Value: "喜欢早班", Revision: 2, Origin: "inferred", Retracted: true}}
	updates := []model.TravelMemoryUpdate{{Op: "add", Text: text("新偏好"), Source: "explicit"}, {Op: "replace", ID: id("missing"), Text: text("未知ID"), Source: "explicit"}, {Op: "retract", ID: id("memory_explicit"), Source: "explicit"}}
	prepared, err := PrepareNativeMemory(rows, updates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Result.Applied) != 2 || len(prepared.Result.Rejected) != 1 || len(prepared.Changes) != 2 || prepared.Changes[1].Value != "优先靠窗" {
		t.Fatalf("partial batch or retraction text preservation lost: %+v", prepared)
	}
	protected := []model.TravelMemoryUpdate{{Op: "replace", ID: id("memory_explicit"), Text: text("改成早班"), Source: "history"}, {Op: "add", Text: text("喜欢早班"), Source: "history"}, {Op: "replace", ID: id("memory_retracted"), Text: text("恢复早班"), Source: "history"}}
	result, err := PrepareNativeMemory(rows, protected, nil)
	if err != nil || len(result.Result.Applied) != 0 || len(result.Result.Rejected) != 3 {
		t.Fatal("inference overrode explicit/retracted memory", result, err)
	}
}
