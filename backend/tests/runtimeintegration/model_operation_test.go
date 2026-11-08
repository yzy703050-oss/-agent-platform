package runtimeintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-platform/backend/internal/client/llm"
	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/repository/platformpg"
	"agent-platform/backend/internal/runtimeports"
	"github.com/cloudwego/eino/schema"
)

func TestBackgroundExecutionLimitsAreSignedAndIndependent(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	limits := runtimev1.LimitSnapshot{MaxModelAttempts: 2, MaxToolAttempts: 1, TokenUpperBound: 56000, MaxModelInputTokens: 20000, MaxModelOutputTokens: 8000, CostUpperMicros: 56000, ToolTimeout: time.Second, DeadlineAt: expiry}
	if _, err := q.f.Platform.Exec(ctx, `INSERT INTO platform.memory_operations(id,user_id,conversation_id,thread_id,context_epoch,kind,request_hash,arguments,base_memory_revision,status,worker_id,lease_epoch,lease_expires_at,phase) VALUES('operation','user','conversation','conversation',1,'periodic_summary','hash','{}',0,'running','maintenance-worker',1,clock_timestamp()+interval '20 seconds','review')`); err != nil {
		t.Fatal(err)
	}
	tx, err := q.f.Maintenance.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: "operation-scope", OwnerService: "maintenance", SubjectID: "user", ConversationID: "conversation", OperationID: "operation", Capabilities: []string{"model"}, Targets: []string{"model:fixture"}, SourceScope: json.RawMessage(`{}`), ContextEpoch: 1, ExpiresAt: expiry, BudgetLimits: control.Reservation{ModelAttempts: 2, ToolAttempts: 1, TokenUpperBound: 56000, CostUpperMicros: 56000}, ExecutionLimits: &limits, ConfigFingerprint: "operation-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ref := runtimev1.ExecRef{ScopeID: scope.ScopeID, ScopeHash: scope.ScopeHash, OwnerKind: "operation", OwnerID: "operation", ThreadID: "conversation", Lease: runtimev1.LeaseRef{WorkerID: "maintenance-worker", Epoch: 1, ExpiresAt: expiry}}
	state, err := platformpg.New(q.f.Maintenance).LoadCall(ctx, ref, "model:review", "model", "model:fixture")
	if err != nil {
		t.Fatal(err)
	}
	if state.Execution.RunID != "" || state.Execution.Limits.TokenUpperBound != 56000 || state.Execution.Fingerprint != "operation-fixture" {
		t.Fatal("background operation borrowed Run limits")
	}
	var hashed bool
	if err = q.f.Admin.QueryRow(ctx, `SELECT scope_hash=encode(sha256(convert_to((to_jsonb(s)-ARRAY['scope_hash','created_at','revoked_at'])::text,'UTF8')),'hex') FROM control.execution_scopes s WHERE scope_id=$1`, scope.ScopeID).Scan(&hashed); err != nil || !hashed {
		t.Fatalf("limits modified after scope signing: %v %v", hashed, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"review result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()
	base, err := llm.NewRuntime(ctx, llm.RuntimeModelConfig{BaseURL: server.URL, ModelID: "fixture", APIKey: "fixture", VerifiedInputBound: true, InputTokenUpperBound: 20000, MaxOutputTokens: 8000, RequestTimeout: time.Second, CleanupGrace: 50 * time.Millisecond, PriceKnown: true, InputMicrosPerMillion: 1000000, OutputMicrosPerMillion: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = llm.NewMetered(base, platformpg.New(q.f.Maintenance), q.permits, []string{"model:fixture"}).Generate(runtimeports.WithCall(ctx, ref, "review:first", 1), []*schema.Message{schema.UserMessage("review")}); err != nil {
		t.Fatal(err)
	}
	var operationOwned bool
	if err = q.f.Admin.QueryRow(ctx, "SELECT run_id IS NULL AND operation_id='operation' FROM platform.messages WHERE message_kind='model_output'").Scan(&operationOwned); err != nil || !operationOwned {
		t.Fatalf("background model fabricated foreground Run output: %v %v", operationOwned, err)
	}
}

func TestForegroundExecutionProjectedBeforeFirstTool(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var projected bool
	if err := q.f.Admin.QueryRow(ctx, `SELECT budget_limits ? 'execution' FROM control.run_guards WHERE source_run_id=(SELECT run_id FROM platform.agent_jobs WHERE id=$1)`, q.ref.OwnerID).Scan(&projected); err != nil || !projected {
		t.Fatalf("Travel cannot load original limits before a root tool call: %v %v", projected, err)
	}
}
