package travel

import (
	pm "agent-platform/backend/internal/model"
	"encoding/json"
	"strings"
	"testing"
)

func TestDemoFactsRemainLabelled(t *testing.T) {
	var plan pm.TripPlan
	if err := json.Unmarshal([]byte(`{"revision":2,"tasks":[{"id":"t1","origin":"上海","destination":"杭州","train":{"candidate":{"name":"演示 G1001","price_fen":10000,"is_demo":true}}}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderTrip(plan), "演示数据") {
		t.Fatal("selected demo price rendered as live fact")
	}
	var evidence pm.TravelEvidence
	if err := json.Unmarshal([]byte(`{"status":"ok","is_demo":true,"candidates":[{"id":"d1","name":"G1001","price_fen":10000,"is_demo":true}]}`), &evidence); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderEvidence(evidence), "演示数据") {
		t.Fatal("demo evidence rendered as live fact")
	}
}
