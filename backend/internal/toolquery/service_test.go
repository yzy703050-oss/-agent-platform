package toolquery

import (
	"context"
	"encoding/json"
	"testing"

	"agent-platform/backend/internal/client/mcp"
	"agent-platform/backend/internal/client/websearch"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
)

func TestWebProviderSchemaAndEmptyEvidence(t *testing.T) {
	var received websearch.Request
	provider := WebProvider("fixture", []string{"web:fixture"}, func(ctx context.Context, in websearch.Request) (websearch.Response, error) {
		received = in
		return websearch.Response{Status: "no_results", Results: []websearch.Result{}}, nil
	})
	for _, raw := range []string{`{"query":"x","scope_id":"forged"}`, `{"query":"x","query":"y"}`, `{"query":"x","time_range":null}`, `{"query":"x","time_range":"today"}`} {
		if err := provider.Validate(json.RawMessage(raw)); err == nil {
			t.Fatalf("unregistered arguments accepted: %s", raw)
		}
	}
	args := json.RawMessage(`{"query":" 差旅 ","time_range":"w"}`)
	if err := provider.Validate(args); err != nil {
		t.Fatal(err)
	}
	result, err := provider.Call(context.Background(), args)
	if err != nil || result.Coverage != "empty" || received.Query != "差旅" || received.TimeRange != "w" || !json.Valid(result.Snapshot["response"]) {
		t.Fatalf("web adapter lost empty result/schema: %+v %+v %v", result, received, err)
	}
}

func TestPolicyProviderPreservesExecutionAndRaw(t *testing.T) {
	raw := json.RawMessage(`{"schema_version":1,"status":"no_knowledge","collection":"fixture","fragments":[],"execution":{"profile":"v3","degraded":true}}`)
	provider := PolicyProvider("fixture", []string{"rag:fixture"}, func(ctx context.Context, key string, in mcp.PolicyQuery) (mcp.EvidenceResponse, error) {
		if key != "tool:policy:first" || in.TopK != 5 {
			t.Fatal("stable key/default arguments lost")
		}
		return mcp.EvidenceResponse{Evidence: model.PolicyEvidence{Status: "no_knowledge", Collection: "fixture"}, Raw: raw, Execution: json.RawMessage(`{"profile":"v3","degraded":true}`)}, nil
	})
	args := json.RawMessage(`{"query":"差旅政策","collection":"fixture"}`)
	if err := provider.Validate(args); err != nil {
		t.Fatal(err)
	}
	ctx := runtimeports.WithCall(context.Background(), runtimev1.ExecRef{}, "tool:policy:first", 1)
	result, err := provider.Call(ctx, args)
	if err != nil || result.Coverage != "empty" || string(result.Snapshot["raw"]) != string(raw) || !json.Valid(result.Snapshot["execution"]) {
		t.Fatalf("policy metadata lost: %+v %v", result, err)
	}
	if err := provider.Validate(json.RawMessage(`{"query":"x","collection":"fixture","retrieval_profile_id":"forged"}`)); err == nil {
		t.Fatal("host selected retrieval profile")
	}
}

func TestTravelProviderRejectsDomainAndDemoMismatch(t *testing.T) {
	provider := TravelProvider("weather", "fixture", "live", []string{"query:fixture"}, func(context.Context, model.TravelQuery) (model.TravelEvidence, error) {
		return model.TravelEvidence{Status: "ok", Candidates: []model.TravelCandidate{{ID: "demo", Name: "demo", IsDemo: true}}}, nil
	})
	if err := provider.Validate(json.RawMessage(`{"domain":"train","city":"北京"}`)); err == nil {
		t.Error("contradictory provider domain accepted")
	}
	if _, err := provider.Call(context.Background(), json.RawMessage(`{"city":"北京"}`)); err == nil {
		t.Error("demo candidate accepted in live evidence")
	}
}
