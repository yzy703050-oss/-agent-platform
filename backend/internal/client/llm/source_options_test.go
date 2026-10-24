package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTravelSourceGenerationOptions(t *testing.T) {
	for _, travel := range []bool{false, true} {
		t.Run(map[bool]string{false: "General unchanged", true: "Travel matches frozen source settings"}[travel], func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))
			defer server.Close()
			constructor := New
			if travel {
				constructor = NewTravel
			}
			m, err := constructor(context.Background(), server.URL, "deepseek-flash", "test-only")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Generate(context.Background(), []*schema.Message{schema.UserMessage("test")}); err != nil {
				t.Fatal(err)
			}
			if !travel {
				if _, ok := body["temperature"]; ok {
					t.Fatal("General generation defaults changed")
				}
				return
			}
			thinking, _ := body["thinking"].(map[string]any)
			if body["temperature"] != 0.3 || body["max_tokens"] != float64(8192) || thinking["type"] != "disabled" {
				t.Fatal("frozen Python generation_options not preserved", body)
			}
		})
	}
}
