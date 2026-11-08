package runtimeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-platform/backend/internal/agenttools"
	"agent-platform/backend/internal/client/hotel"
	"agent-platform/backend/internal/client/llm"
	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/client/train"
	"agent-platform/backend/internal/client/websearch"
	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/permit"
	"agent-platform/backend/internal/repository/platformpg"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/toolquery"
	"github.com/cloudwego/eino/schema"
)

type queryFixture struct {
	f       *runtimeFixture
	repo    *platformpg.Repository
	ref     runtimev1.ExecRef
	permits *permit.Redis
}

func TestDirectQueryTrainKnownPrice(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error_code":0,"result":[{"train_no":"G1","departure_station":"北京南","arrival_station":"上海虹桥","departure_time":"08:00","arrival_time":"12:00","duration":"04:00","enable_booking":"1","prices":[{"seat_name":"二等座","seat_type_code":"O","price":"553.5","num":"5"}]}]}`)
	}))
	defer server.Close()
	client := train.New("fixture-key", providerhttp.SingleAttempt(&rewriteProvider{target: server.URL, client: server.Client()}), time.Now)
	args, _ := json.Marshal(model.TravelQuery{Domain: "train", Origin: "北京南", Destination: "上海虹桥", Date: time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")})
	input := toolquery.QueryInput{CallKey: "train:first", Domain: "train", Mode: "live", Provider: "fixture", Arguments: args}
	result, err := queryService(t, q, toolquery.TravelProvider("train", "fixture", "live", []string{"query:fixture"}, client.Search)).Execute(ctx, q.ref, input)
	if err != nil {
		t.Fatal(err)
	}
	var candidates []runtimev1.Candidate
	if err = json.Unmarshal(result.Snapshot["candidates"], &candidates); err != nil || len(candidates) != 1 || candidates[0].Price == nil || candidates[0].Price.AmountMinor != 55350 || candidates[0].Price.Basis != "person" {
		t.Fatalf("known train price invalid/lost: %+v %v", candidates, err)
	}
}

type queryRoundTrip func(*http.Request) (*http.Response, error)

func (f queryRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDirectQueryRuntimeWebNoAutomaticRetry(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "failure_once", false: "saved_success"}[fail], func(t *testing.T) {
			q := newQueryFixture(t)
			ctx := context.Background()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if fail {
					w.WriteHeader(503)
					return
				}
				io.WriteString(w, `<div id="links"><div class="web-result"><h2 class="result__title"><a href="https://example.org">Eino</a></h2><a class="result__snippet">docs</a></div><div class="nav-link"><form><input type="hidden" name="s" value="1"></form></div></div>`)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			client, err := websearch.NewRuntime(ctx, queryRoundTrip(func(req *http.Request) (*http.Response, error) {
				copy := req.Clone(req.Context())
				u := *req.URL
				u.Scheme = target.Scheme
				u.Host = target.Host
				copy.URL = &u
				return http.DefaultTransport.RoundTrip(copy)
			}))
			if err != nil {
				t.Fatal(err)
			}
			service := queryService(t, q, toolquery.WebProvider("fixture", []string{"query:fixture"}, client.Search))
			tool := agenttools.NewRuntimeWebSearchTool(service, "fixture", time.Second)
			info, _ := tool.Info(ctx)
			if info.Name != "web_search" {
				t.Fatal("registered tool name changed")
			}
			call := runtimeports.WithCall(ctx, q.ref, "web:first", 1)
			output, err := tool.InvokableRun(call, `{"query":"Eino"}`)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatalf("web performed hidden retry/pagination: %d", requests.Load())
			}
			if fail {
				if !strings.Contains(output, `"status":"search_unavailable"`) {
					t.Fatalf("failure fabricated result: %s", output)
				}
			} else {
				if !strings.Contains(output, `"status":"ok"`) {
					t.Fatalf("success lost: %s", output)
				}
				if _, err = tool.InvokableRun(call, `{"query":"Eino"}`); err != nil || requests.Load() != 1 {
					t.Fatal("saved web query repeated HTTP")
				}
			}
		})
	}
}

func newQueryFixture(t *testing.T) queryFixture {
	t.Helper()
	cfg := entryConfig()
	cfg.ScopeTargets = append(cfg.ScopeTargets, "hotel", "train", "web", "policy")
	f, repo, api, scope := entryFixture(t, cfg)
	admitEntry(t, api, scope)
	lease := claimEntry(t, repo, "query-worker")
	if _, err := f.Admin.Exec(context.Background(), "INSERT INTO control.resource_limits(resource_key,max_concurrency) VALUES('query:fixture',2),('model:fixture',2)"); err != nil {
		t.Fatal(err)
	}
	_, redisCfg := redisFixture(t, f)
	permits, err := permit.NewRedis(f.Platform, redisCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { permits.Close() })
	return queryFixture{f, repo, lease.Ref, permits}
}

func queryInput() toolquery.QueryInput {
	return toolquery.QueryInput{CallKey: "weather:first", Domain: "weather", Provider: "fixture", Mode: "live", Arguments: json.RawMessage(`{"city":"北京"}`)}
}

func queryProvider(client *http.Client, url string) toolquery.Provider {
	return toolquery.Provider{Domain: "weather", Name: "fixture", Mode: "live", ResourceKeys: []string{"query:fixture"}, Validate: toolquery.ValidateObject,
		Call: func(ctx context.Context, args json.RawMessage) (toolquery.Result, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			var data map[string]json.RawMessage
			if err := providerhttp.Decode(ctx, providerhttp.SingleAttempt(client), req, &data); err != nil {
				return toolquery.Result{}, err
			}
			return toolquery.Result{Snapshot: data, Coverage: "complete", FetchedAt: time.Now().UTC()}, nil
		}}
}

func queryService(t *testing.T, q queryFixture, provider toolquery.Provider) *toolquery.Service {
	t.Helper()
	service, err := toolquery.New(q.repo, q.permits, []toolquery.Provider{provider}, toolquery.Options{CleanupGrace: 50 * time.Millisecond, Jitter: func(time.Duration) time.Duration { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestDirectQueryHasNoPlan(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"temperature_c":12,"source":"fixture receiver"}`))
	}))
	defer server.Close()
	service := queryService(t, q, queryProvider(server.Client(), server.URL))
	evidence, err := service.Execute(ctx, q.ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ID == "" || evidence.ToolCallRecordID == "" || evidence.ResultRevision != 1 || requests.Load() != 1 {
		t.Fatalf("query not backed by an actual attempt: %+v requests=%d", evidence, requests.Load())
	}
	for _, table := range []string{"travel.plans", "platform.agent_delegations"} {
		var n int
		if err = q.f.Admin.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("query created planning data: %s=%d err=%v", table, n, err)
		}
	}
	var calls, attempts, proofs int
	if err = q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform.tool_calls),(SELECT count(*) FROM control.outbound_calls),(SELECT count(*) FROM platform.tool_evidence)").Scan(&calls, &attempts, &proofs); err != nil || calls != 1 || attempts != 1 || proofs != 1 {
		t.Fatalf("missing original facts: %d/%d/%d %v", calls, attempts, proofs, err)
	}
}

func TestSavedEvidenceNoRecompute(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[]}`))
	}))
	defer server.Close()
	provider := queryProvider(server.Client(), server.URL)
	first, err := queryService(t, q, provider).Execute(ctx, q.ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	// A new service has no process-local query cache.
	second, err := queryService(t, q, provider).Execute(ctx, q.ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.ResultRevision != first.ResultRevision || requests.Load() != 1 {
		t.Fatal("saved successful empty evidence recomputed")
	}
	changed := queryInput()
	changed.Arguments = json.RawMessage(`{"city":"上海"}`)
	if _, err = queryService(t, q, provider).Execute(ctx, q.ref, changed); err == nil || requests.Load() != 1 {
		t.Fatal("same call key accepted different immutable arguments")
	}
	if err = platformpg.New(q.f.Platform).RevokeSession(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err = queryService(t, q, provider).Execute(ctx, q.ref, queryInput()); err == nil || requests.Load() != 1 {
		t.Fatal("saved result bypassed current revocation")
	}
}

type rewriteProvider struct {
	target string
	client *http.Client
}

func (d rewriteProvider) Do(req *http.Request) (*http.Response, error) {
	next := req.Clone(req.Context())
	url, _ := http.NewRequest(req.Method, d.target, nil)
	next.URL = url.URL
	return d.client.Do(next)
}

func TestDirectQueryHotelUnknownPrice(t *testing.T) {
	q := newQueryFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"1","infocode":"10000","pois":[{"id":"hotel-a","name":"真实地点候选","typecode":"100100","cityname":"北京市","address":"北京"}]}`))
	}))
	defer server.Close()
	client := hotel.New("fixture-only", providerhttp.SingleAttempt(rewriteProvider{server.URL, server.Client()}), time.Now)
	provider := toolquery.TravelProvider("hotel", "amap", "live", []string{"query:fixture"}, client.Search)
	input := toolquery.QueryInput{CallKey: "hotel:first", Domain: "hotel", Provider: "amap", Mode: "live", Arguments: json.RawMessage(`{"city":"北京"}`)}
	evidence, err := queryService(t, q, provider).Execute(context.Background(), q.ref, input)
	if err != nil {
		t.Fatal(err)
	}
	var candidates []runtimev1.Candidate
	if err = json.Unmarshal(evidence.Snapshot["candidates"], &candidates); err != nil || len(candidates) != 1 || candidates[0].Price != nil {
		t.Fatalf("invented hotel price: %+v %v", candidates, err)
	}
	if raw := candidates[0].Attributes["booked"]; len(raw) > 0 && string(raw) != "false" {
		t.Fatal("invented booking")
	}
}

func TestRetryFourAttemptsAcrossRestart(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	in := queryInput()
	state, err := q.repo.BeginQuery(ctx, q.ref, in)
	if err != nil {
		t.Fatal(err)
	}
	key := "tool:" + in.CallKey
	id := runtimeports.AttemptID(q.ref, key, 1)
	hold := state.DeadlineAt.Add(50 * time.Millisecond)
	if err = q.permits.Acquire(ctx, "query:fixture", id, hold); err != nil {
		t.Fatal(err)
	}
	if _, err = q.repo.Reserve(ctx, q.ref, control.AttemptInput{AttemptID: id, LogicalKey: key, AttemptNo: 1, Capability: "tool", Target: "weather", ResourceKeys: []string{"query:fixture"}, Reservation: control.Reservation{ToolAttempts: 1}, HoldUntil: hold, InputHash: state.Record.InputHash}); err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err = q.repo.Settle(ctx, q.ref, control.Settlement{AttemptID: id, Status: "failed", Usage: &control.Reservation{ToolAttempts: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.repo.ScheduleRetry(ctx, q.ref, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = q.permits.Release(ctx, "query:fixture", id); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the repository and service after the first durable attempt.
	q.repo, err = platformpg.NewRuntime(q.f.Platform, entryConfig())
	if err != nil {
		t.Fatal(err)
	}
	service := queryService(t, q, queryProvider(server.Client(), server.URL))
	if _, err = service.Execute(ctx, q.ref, in); err == nil {
		t.Fatal("four failed attempts reported success")
	}
	if requests.Load() != 4 {
		t.Fatalf("restart reset/multiplied physical attempts: %d", requests.Load())
	}
	if _, err = service.Execute(ctx, q.ref, in); err == nil || requests.Load() != 4 {
		t.Fatal("terminal failed call retried again")
	}
	var logical, physical, retries int
	if err = q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform.tool_calls),(SELECT count(*) FROM control.outbound_calls),(SELECT retry_count FROM platform.tool_calls LIMIT 1)").Scan(&logical, &physical, &retries); err != nil || logical != 1 || physical != 4 || retries != 3 {
		t.Fatalf("retry facts: logical=%d physical=%d retries=%d err=%v", logical, physical, retries, err)
	}
}

func TestSavedEvidenceCommitWindow(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"temperature_c":12}`))
	}))
	defer server.Close()
	if _, err := q.f.Admin.Exec(ctx, `CREATE FUNCTION platform.fail_query_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture evidence storage failed'; END $$; CREATE TRIGGER fail_query_commit BEFORE INSERT ON platform.tool_evidence FOR EACH ROW EXECUTE FUNCTION platform.fail_query_commit()`); err != nil {
		t.Fatal(err)
	}
	provider := queryProvider(server.Client(), server.URL)
	if _, err := queryService(t, q, provider).Execute(ctx, q.ref, queryInput()); err == nil {
		t.Fatal("failed fact storage reported successful query")
	}
	if _, err := q.f.Admin.Exec(ctx, "DROP TRIGGER fail_query_commit ON platform.tool_evidence"); err != nil {
		t.Fatal(err)
	}
	evidence, err := queryService(t, q, provider).Execute(ctx, q.ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("unsaved read-only result not correctly recomputed: %d", requests.Load())
	}
	replayed, err := queryService(t, q, provider).Execute(ctx, q.ref, queryInput())
	if err != nil || replayed.ID != evidence.ID || requests.Load() != 2 {
		t.Fatalf("committed fact recomputed: %d %v", requests.Load(), err)
	}
}

func TestDirectQueryUnknownNetworkRetainsPermit(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	service := queryService(t, q, queryProvider(server.Client(), server.URL))
	if _, err := service.Execute(ctx, q.ref, queryInput()); err == nil {
		t.Fatal("lost response reported success")
	}
	var status string
	if err := q.f.Admin.QueryRow(ctx, "SELECT status FROM control.outbound_calls ORDER BY attempt_no LIMIT 1").Scan(&status); err != nil || status != "unknown" || requests.Load() != 1 {
		t.Fatalf("unknown call released/retried: status=%s requests=%d err=%v", status, requests.Load(), err)
	}
	if _, err := service.Execute(ctx, q.ref, queryInput()); err == nil || requests.Load() != 1 {
		t.Fatal("uncertain original physical call was sent again")
	}
	id := runtimeports.AttemptID(q.ref, "tool:"+queryInput().CallKey, 1)
	if err := q.permits.Release(ctx, "query:fixture", id); err != permit.ErrHeld {
		t.Fatalf("unknown permit released: %v", err)
	}
}

func TestMeteredModelPreservesWithToolsAndSavedOutput(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		defer r.Body.Close()
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body["tools"]) == 0 || string(body["max_tokens"]) != "8000" {
			t.Errorf("tool binding/output hard limit lost: %s", body["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"已准备查询","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"北京\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`))
	}))
	defer server.Close()
	base, err := llm.NewRuntime(ctx, llm.RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture-only", VerifiedInputBound: true, InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: 50 * time.Millisecond, PriceKnown: true, InputMicrosPerMillion: 1000000, OutputMicrosPerMillion: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	tool := &schema.ToolInfo{Name: "weather", Desc: "天气", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"city": {Type: schema.String, Required: true}})}
	bound, err := llm.NewMetered(base, q.repo, q.permits, []string{"model:fixture"}).WithTools([]*schema.ToolInfo{tool})
	if err != nil {
		t.Fatal(err)
	}
	tool.Name = "mutated-after-binding"
	call := runtimeports.WithCall(ctx, q.ref, "planner:first", 1)
	input := []*schema.Message{schema.UserMessage("北京天气")}
	first, err := bound.Generate(call, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call-1" {
		t.Fatal("complete model tool call was not preserved")
	}
	if first.ResponseMeta == nil || first.ResponseMeta.Usage == nil || first.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 2 || first.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 1 {
		t.Fatal("complete model usage metadata was discarded")
	}
	var saved int
	if err = q.f.Admin.QueryRow(ctx, "SELECT count(*) FROM platform.messages WHERE message_kind='model_output'").Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("model output returned before persistence: %d %v", saved, err)
	}
	tool.Name = "weather"
	second, err := bound.Generate(call, input)
	if err != nil || len(second.ToolCalls) != 1 || requests.Load() != 1 {
		t.Fatalf("saved output repeated HTTP: %d %v", requests.Load(), err)
	}
	if _, err = bound.Generate(call, []*schema.Message{schema.UserMessage("输入已变更")}); err == nil || requests.Load() != 1 {
		t.Fatal("stable model call key accepted different input")
	}
	var models, tokens, cost int
	if err = q.f.Admin.QueryRow(ctx, "SELECT sum((reservation->>'model_attempts')::int),sum((usage->>'token_upper_bound')::int),sum((usage->>'cost_upper_micros')::int) FROM control.outbound_calls").Scan(&models, &tokens, &cost); err != nil || models != 1 || tokens != 3 || cost != 3 {
		t.Fatalf("model accounting mismatch: %d/%d/%d %v", models, tokens, cost, err)
	}
	if err = platformpg.New(q.f.Platform).RevokeSession(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Generate(call, input); err == nil || requests.Load() != 1 {
		t.Fatal("saved model output bypassed current authorization")
	}
}

func TestMeteredModelStreamPersistenceAndTruncation(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "truncated"}[complete], func(t *testing.T) {
			q := newQueryFixture(t)
			ctx := context.Background()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"草稿\"},\"finish_reason\":null}]}\n\n")
				w.(http.Flusher).Flush()
				if complete {
					io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			base, err := llm.NewRuntime(ctx, llm.RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture", VerifiedInputBound: true, InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: 50 * time.Millisecond, PriceKnown: true, InputMicrosPerMillion: 1000000, OutputMicrosPerMillion: 1000000})
			if err != nil {
				t.Fatal(err)
			}
			metered := llm.NewMetered(base, q.repo, q.permits, []string{"model:fixture"})
			call := runtimeports.WithCall(ctx, q.ref, "stream:first", 1)
			reader, err := metered.Stream(call, []*schema.Message{schema.UserMessage("test")})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var text string
			var finalErr error
			for {
				chunk, err := reader.Recv()
				if err != nil {
					finalErr = err
					break
				}
				if chunk != nil {
					text += chunk.Content
				}
			}
			if text != "草稿" {
				t.Fatalf("stream draft lost: %q", text)
			}
			var saved int
			var status string
			if err = q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform.messages WHERE message_kind='model_output'),(SELECT status FROM control.outbound_calls LIMIT 1)").Scan(&saved, &status); err != nil {
				t.Fatal(err)
			}
			if complete {
				if !errors.Is(finalErr, io.EOF) || saved != 1 || status != "succeeded" {
					t.Fatalf("EOF before durable complete output: %d %s %v", saved, status, finalErr)
				}
				output, err := metered.Generate(call, []*schema.Message{schema.UserMessage("test")})
				if err != nil || output.Content != "草稿" || requests.Load() != 1 {
					t.Fatalf("stream replay recomputed: %d %v", requests.Load(), err)
				}
			} else {
				if errors.Is(finalErr, io.EOF) || saved != 0 || status != "unknown" || requests.Load() != 1 {
					t.Fatalf("partial stream accepted/retried: %d %s %d %v", saved, status, requests.Load(), finalErr)
				}
			}
		})
	}
}

func TestMeteredModelRetryFourAndUnknownUsage(t *testing.T) {
	for _, retry := range []bool{true, false} {
		t.Run(map[bool]string{true: "retry", false: "unknown_usage"}[retry], func(t *testing.T) {
			q := newQueryFixture(t)
			ctx := context.Background()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if retry {
					w.WriteHeader(503)
					io.WriteString(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
				} else {
					io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			base, err := llm.NewRuntime(ctx, llm.RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture", VerifiedInputBound: true, InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: 50 * time.Millisecond, PriceKnown: true, InputMicrosPerMillion: 1000000, OutputMicrosPerMillion: 1000000})
			if err != nil {
				t.Fatal(err)
			}
			call := runtimeports.WithCall(ctx, q.ref, "retry:first", 1)
			metered := llm.NewMetered(base, q.repo, q.permits, []string{"model:fixture"})
			_, err = metered.Generate(call, []*schema.Message{schema.UserMessage("test")})
			if retry {
				if !errors.Is(err, runtimeports.ErrRetryExhausted) || requests.Load() != 4 {
					t.Fatalf("model physical retries: %d %v", requests.Load(), err)
				}
				_, err = llm.NewMetered(base, q.repo, q.permits, []string{"model:fixture"}).Generate(call, []*schema.Message{schema.UserMessage("test")})
				if !errors.Is(err, runtimeports.ErrRetryExhausted) || requests.Load() != 4 {
					t.Fatalf("restart reset retries: %d %v", requests.Load(), err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var usageNull bool
				var reserved int
				if err = q.f.Admin.QueryRow(ctx, "SELECT usage IS NULL,(reservation->>'token_upper_bound')::int FROM control.outbound_calls LIMIT 1").Scan(&usageNull, &reserved); err != nil || !usageNull || reserved != 28000 {
					t.Fatalf("unknown usage refunded: %v %d %v", usageNull, reserved, err)
				}
			}
		})
	}
}
