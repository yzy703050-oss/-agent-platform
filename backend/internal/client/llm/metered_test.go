package llm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestMeteredRejectsUnverifiedBoundsAndMissingContext(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	cfg := RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture", InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: time.Second}
	if _, err := NewRuntime(context.Background(), cfg); err == nil {
		t.Fatal("context estimate accepted as provider hard bound")
	}
	cfg.VerifiedInputBound = true
	base, err := NewRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewMetered(base, nil, nil, []string{"model:fixture"}).Generate(context.Background(), []*schema.Message{schema.UserMessage("test")}); err == nil {
		t.Fatal("model called without program execution identity")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid invocation reached network")
	}
	if _, err = cost(math.MaxInt64, 1, math.MaxInt64, 1); err == nil {
		t.Fatal("cost overflow accepted")
	}
	price, err := cost(1, 1, 1, 1)
	if err != nil || price != 2 {
		t.Fatalf("cost did not round conservatively: %d %v", price, err)
	}
}
