package travel

import "testing"

func TestTravelSourceStructuredJSON(t *testing.T) {
	for _, text := range []string{"说明：```json\n{\"status\":\"ok\"}\n```", `{'status':'ok'}`, `{"status":"ok",}`, "{\"status\":\"o\nk\"}"} {
		var out struct {
			Status string `json:"status"`
		}
		if err := parseAuxiliaryJSON(text, &out); err != nil || out.Status != "ok" {
			t.Fatal("source JSON recovery changed", text, out, err)
		}
	}
	var out any
	if err := parseAuxiliaryJSON("没有结构化结果", &out); err == nil {
		t.Fatal("invalid response was invented as JSON")
	}
}
