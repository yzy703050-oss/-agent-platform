package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHTTPResultLimitAndScope(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "scope_and_reuse", true: "whole_envelope_limit"}[large], func(t *testing.T) {
			var gets, calls, initializes atomic.Int32
			var connections sync.Map
			server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "v1"}, nil)
			sdk.AddTool(server, &sdk.Tool{Name: "query_policy_evidence"}, func(ctx context.Context, req *sdk.CallToolRequest, args map[string]any) (*sdk.CallToolResult, any, error) {
				calls.Add(1)
				if req.Params.Meta["xiaodoubao/runtime"] == nil || args["scope_id"] != nil || args["query"] != "差旅政策" {
					t.Error("trusted metadata/arguments crossed boundary")
				}
				result := &sdk.CallToolResult{StructuredContent: map[string]any{"schema_version": 1, "status": "no_knowledge", "collection": "fixture", "fragments": []any{}, "execution": map[string]any{"degraded": true, "profile_version": "v7"}}}
				// The policy JSON is tiny; only the ignored RPC metadata exceeds the cap.
				if large {
					result.Meta = sdk.Meta{"padding": strings.Repeat("x", 1000000)}
				}
				return result, nil, nil
			})
			handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Error("missing service credential")
					w.WriteHeader(401)
					return
				}
				if r.Method == "GET" {
					gets.Add(1)
					w.WriteHeader(405)
					return
				}
				connections.Store(r.RemoteAddr, true)
				if r.Method == "POST" {
					body, _ := io.ReadAll(r.Body)
					r.Body.Close()
					r.Body = io.NopCloser(bytes.NewReader(body))
					var envelope struct {
						Method string `json:"method"`
					}
					json.Unmarshal(body, &envelope)
					if envelope.Method == "initialize" {
						initializes.Add(1)
					}
				}
				handler.ServeHTTP(w, r)
			}))
			defer receiver.Close()
			client, err := NewHTTPClient(HTTPConfig{Endpoint: receiver.URL, ServiceID: "platform", BearerToken: "fixture-secret", Collections: []string{"fixture"}, RequestTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ref := runtimev1.ExecRef{ScopeID: "scope", ScopeHash: strings.Repeat("a", 64), OwnerKind: "job", OwnerID: "job", ThreadID: "thread", Lease: runtimev1.LeaseRef{WorkerID: "worker", Epoch: 1, ExpiresAt: time.Now().Add(time.Minute)}}
			execution := runtimeports.Execution{Scope: control.AuthorizedScope{ScopeInput: control.ScopeInput{ScopeID: ref.ScopeID, OwnerService: "platform", SubjectID: "user", ConversationID: "conversation", SourceRunID: "run", ContextEpoch: 1, Capabilities: []string{"tool"}, Targets: []string{"policy"}, ExpiresAt: time.Now().Add(time.Minute)}, ScopeHash: ref.ScopeHash}, Service: "platform", UserID: "user", ConversationID: "conversation", RunID: "run", ContextEpoch: 1, Limits: runtimev1.LimitSnapshot{DeadlineAt: time.Now().Add(time.Minute)}}
			call := runtimeports.WithExecution(runtimeports.WithCall(context.Background(), ref, "tool:policy:first", 1), execution)
			query := PolicyQuery{Query: "差旅政策", Collection: "fixture", TopK: 5}
			result, err := client.CallEvidence(call, "tool:policy:first", query)
			if large {
				if err == nil {
					t.Fatal("oversized RPC shell accepted")
				}
			} else {
				if err != nil || result.Evidence.Status != "no_knowledge" || !json.Valid(result.Raw) || !strings.Contains(string(result.Execution), "v7") {
					t.Fatalf("full response lost: %+v %v", result, err)
				}
				if _, err = client.CallEvidence(call, "tool:policy:first", query); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 2 {
					t.Fatalf("calls missing: %d", calls.Load())
				}
				count := 0
				connections.Range(func(any, any) bool { count++; return true })
				if initializes.Load() != 1 || count > 2 {
					t.Fatalf("service session/HTTP pool not reused: initialization=%d sockets=%d", initializes.Load(), count)
				}
			}
			before := calls.Load()
			badRef := ref
			badRef.ScopeHash = strings.Repeat("b", 64)
			bad := runtimeports.WithExecution(runtimeports.WithCall(context.Background(), badRef, "tool:policy:first", 1), execution)
			if _, err = client.CallEvidence(bad, "tool:policy:first", query); err == nil || calls.Load() != before {
				t.Fatal("mismatched execution scope reached HTTP")
			}
			if gets.Load() != 0 {
				t.Fatalf("standalone SSE attempted: %d", gets.Load())
			}
		})
	}
}
