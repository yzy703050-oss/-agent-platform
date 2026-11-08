package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestRuntimeHTTPHasOnePhysicalSendAndBoundsWholeResponse(t *testing.T) {
	for _, status := range []int{503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + strings.Repeat("a", 1000001) + `"}}]}`))
				}
			}))
			defer server.Close()
			base, err := NewRuntime(context.Background(), RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture", VerifiedInputBound: true, InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = base.Generate(context.Background(), []*schema.Message{schema.UserMessage("test")}); err == nil {
				t.Fatal("failed/oversized response accepted")
			}
			if requests.Load() != 1 {
				t.Fatalf("hidden physical retries: %d", requests.Load())
			}
		})
	}
}
