package runtimeintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/handler"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/repository/travelpg"
	"agent-platform/backend/internal/toolquery"
)

func TestCrossServiceEvidenceIdentityAndRevokedSource(t *testing.T) {
	q := newQueryFixture(t)
	ctx := context.Background()
	var messageID string
	if err := q.f.Admin.QueryRow(ctx, "SELECT id FROM platform.messages WHERE message_kind='user_request' LIMIT 1").Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	input := queryInput()
	input.SourceRefs = []runtimev1.SourceRef{{Kind: "message", ID: messageID}}
	provider := toolquery.Provider{Domain: "weather", Name: "fixture", Mode: "live", ResourceKeys: []string{"query:fixture"}, Validate: toolquery.ValidateObject, Call: func(context.Context, json.RawMessage) (toolquery.Result, error) {
		return toolquery.Result{Snapshot: map[string]json.RawMessage{"candidates": json.RawMessage(`[{"candidate_id":"original-candidate","title":"forecast","price":null,"attributes":{},"source_url":null,"missing_fields":[]}]`)}, Coverage: "complete", FetchedAt: time.Now().UTC()}, nil
	}}
	record, err := queryService(t, q, provider).Execute(ctx, q.ref, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record)
	hash, err := evidence.ContentHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	locator := evidence.Locator{OriginService: "platform", OriginID: record.ID, OriginRevision: 1, ContentHash: hash}
	stored, _, err := q.repo.LocalOrImport(ctx, q.ref, record.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	storedRaw, _ := json.Marshal(stored.Record)
	storedHash, _ := evidence.ContentHash(storedRaw)
	if storedHash != hash {
		t.Fatalf("first result differs from committed immutable fact: fetched %s / %s", record.FetchedAt, stored.Record.FetchedAt)
	}
	manifest, _ := json.Marshal(map[string]any{"evidence": []evidence.Locator{locator}})
	tx, err := q.f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: "travel-scope", OwnerService: "travel", SubjectID: "user", ConversationID: "conversation", SourceRunID: recordRunID(t, q), DelegationID: "delegation", ContextEpoch: 1, ExpiresAt: time.Now().Add(time.Minute), Capabilities: []string{"evidence", "model", "tool"}, Targets: []string{"weather", "model:fixture"}, SourceScope: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.f.Travel.Exec(ctx, `INSERT INTO travel.threads(id,user_id,conversation_id,origin_thread_id,last_main_context_epoch) VALUES('travel-thread','user','conversation','conversation',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.f.Travel.Exec(ctx, `INSERT INTO travel.tasks(id,caller_id,subject_id,conversation_id,source_run_id,delegation_id,request_key,input_hash,arguments,scope_id,scope_hash,thread_id,status,worker_id,lease_epoch,lease_expires_at,deadline_at) VALUES('travel-task','platform','user','conversation',$1,'delegation','request','hash','{}',$2,$3,'travel-thread','running','travel-worker',1,clock_timestamp()+interval '20 seconds',clock_timestamp()+interval '1 minute')`, scope.SourceRunID, scope.ScopeID, scope.ScopeHash); err != nil {
		t.Fatal(err)
	}
	ref := runtimev1.ExecRef{ScopeID: scope.ScopeID, ScopeHash: scope.ScopeHash, OwnerKind: "task", OwnerID: "travel-task", ThreadID: "travel-thread", Lease: runtimev1.LeaseRef{WorkerID: "travel-worker", Epoch: 1, ExpiresAt: time.Now().Add(20 * time.Second)}}
	endpoint, err := handler.NewInternalEvidence(q.repo, []handler.ServiceCredential{{ServiceID: "travel", Token: "fixture-transfer-token"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(endpoint)
	defer server.Close()
	consumer := travelpg.New(q.f.Travel)
	reader, err := evidence.NewReader(consumer, []evidence.Remote{{ServiceID: "platform", Slug: "platform", Endpoint: server.URL, BearerToken: "fixture-transfer-token"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	view, err := reader.Import(ctx, ref, locator)
	if err != nil {
		t.Fatal(err)
	}
	candidateHash, _ := evidence.ContentHash(view.Record.Snapshot["candidates"])
	originalCandidateHash, _ := evidence.ContentHash(record.Snapshot["candidates"])
	if view.Record.ID != record.ID || view.ReferenceID == record.ID || len(view.ReferenceID) > 160 || candidateHash != originalCandidateHash {
		t.Fatal("remote evidence identity/candidate changed")
	}
	replay, err := reader.Resolve(ctx, ref, view.ReferenceID, 1)
	if err != nil || replay.ReferenceID != view.ReferenceID {
		t.Fatalf("verified import replay failed: %v", err)
	}
	var ignored []byte
	if err = q.f.Platform.QueryRow(ctx, "SELECT control.peer_evidence_context($1,$2,NULL,$3,1,$4)", ref.ScopeID, ref.ScopeHash, record.ID, hash).Scan(&ignored); err == nil {
		t.Fatal("missing authenticated peer accepted")
	}
	var imports, calls int
	if err = q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel.evidence_imports),(SELECT count(*) FROM travel.tool_calls)").Scan(&imports, &calls); err != nil || imports != 1 || calls != 0 {
		t.Fatalf("import fabricated local query: %d %d %v", imports, calls, err)
	}
	wrong := locator
	wrong.ContentHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err = reader.Import(ctx, ref, wrong); err == nil {
		t.Fatal("altered identity/hash accepted")
	}
	weatherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"temperature_c":12}`))
	}))
	defer weatherServer.Close()
	queries, err := toolquery.New(consumer, q.permits, []toolquery.Provider{queryProvider(weatherServer.Client(), weatherServer.URL)}, toolquery.Options{CleanupGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	travelEvidence, err := queries.Execute(ctx, ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	var own bool
	if err = q.f.Admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM travel.tool_evidence WHERE id=$1)", travelEvidence.ID).Scan(&own); err != nil || !own {
		t.Fatalf("specialist query wrote another service's fact: %v %v", own, err)
	}
	if _, err = q.f.Admin.Exec(ctx, "DELETE FROM platform.messages WHERE id=$1", messageID); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Resolve(ctx, ref, view.ReferenceID, 1); err == nil {
		t.Fatal("cached import bypassed withdrawn source")
	}
}

func recordRunID(t *testing.T, q queryFixture) string {
	t.Helper()
	var id string
	if err := q.f.Admin.QueryRow(context.Background(), "SELECT run_id FROM platform.agent_jobs WHERE id=$1", q.ref.OwnerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
