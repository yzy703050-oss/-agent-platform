package runtimeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"agent-platform/backend/internal/client/mcp"
	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/client/train"
	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/handler"
	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/repository/travelpg"
	"agent-platform/backend/internal/runtimeprotocol"
	"agent-platform/backend/internal/service/travelplan"
	"agent-platform/backend/internal/toolquery"
	"github.com/jackc/pgx/v5/pgconn"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type planFixture struct {
	q          queryFixture
	ref        v1.ExecRef
	service    *travelplan.Service
	definition v1.PlanDefinition
}

func readyDefinition(f planFixture) v1.PlanDefinition {
	raw, _ := json.Marshal(f.definition)
	var d v1.PlanDefinition
	json.Unmarshal(raw, &d)
	h := &d.Tasks[0].Parameters.Hotel.Value
	h.CheckIn = v1.Field[string]{Present: true, Value: "2026-11-10"}
	h.CheckOut = v1.Field[string]{Present: true, Value: "2026-11-12"}
	h.Guests = v1.Field[int64]{Present: true, Value: 1}
	return d
}
func fixturePlanPatch(t *testing.T, plan string, revision int64, ops string) v1.PlanPatch {
	t.Helper()
	var operations []v1.PatchOperation
	if err := json.Unmarshal([]byte(ops), &operations); err != nil {
		t.Fatal(err)
	}
	return v1.PlanPatch{SchemaVersion: 1, PlanID: plan, ExpectedRevision: revision, Reason: "fixture mutation", Operations: operations}
}

func TestTaskDefinitionInvalidatesAllItsItems(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	d := readyDefinition(f)
	d.Tasks[0].Parameters.Web = v1.Field[v1.WebParameters]{Present: true, Value: v1.WebParameters{Query: v1.Field[string]{Present: true, Value: "上海酒店信息"}}}
	d.Tasks[0].Items = append(d.Tasks[0].Items, v1.QueryItem{ItemID: "item_web", Domain: "web", DependsOn: []string{}})
	d.Tasks = append(d.Tasks, v1.TaskDefinition{TaskID: "task_weather", Title: "天气", Target: v1.TaskTarget{Kind: "plan"}, Parameters: v1.TaskParameters{Weather: v1.Field[v1.WeatherParameters]{Present: true, Value: v1.WeatherParameters{City: v1.Field[string]{Present: true, Value: "上海"}, Date: v1.Field[string]{Present: true, Value: "2026-11-10"}}}}, RequirementIDs: []string{}, Ranking: []string{}, Items: []v1.QueryItem{{ItemID: "item_weather", Domain: "weather", DependsOn: []string{}}}})
	created, err := f.service.Create(ctx, f.ref, "create:first", d)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f.service.ApplyPatch(ctx, f.ref, "patch:first", fixturePlanPatch(t, created.PlanID, 1, `[{"op":"update_task","task_id":"task_hotel","changes":{"set":{"parameters.hotel.check_out":"2026-11-13"}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.InvalidatedTaskIDs) != 1 || changed.InvalidatedTaskIDs[0] != "task_hotel" || view.Snapshot.TaskVersions[0].TaskRevision != 2 || view.Snapshot.TaskVersions[1].TaskRevision != 1 {
		t.Fatal("task versions changed beyond affected task")
	}
	var stale, fresh, untouched int
	if err = f.q.f.Admin.QueryRow(ctx, `SELECT count(*) FILTER(WHERE task_id='task_hotel' AND task_revision=1 AND status='stale'),count(*) FILTER(WHERE task_id='task_hotel' AND task_revision=2 AND generation=1 AND status='pending'),count(*) FILTER(WHERE task_id='task_weather' AND task_revision=1 AND generation=1 AND status='pending') FROM travel.execution_steps`).Scan(&stale, &fresh, &untouched); err != nil || stale != 2 || fresh != 2 || untouched != 1 {
		t.Fatalf("wrong whole-task invalidation: %d/%d/%d %v", stale, fresh, untouched, err)
	}
}

func preparePlanQuery(t *testing.T, f planFixture, plan string, expiries ...time.Duration) (v1.ExecutionRequest, evidence.View) {
	t.Helper()
	ctx := context.Background()
	var request v1.ExecutionRequest
	var raw []byte
	if err := f.q.f.Admin.QueryRow(ctx, `SELECT id,plan_revision,task_id,task_revision,item_id,generation,domain,input,input_hash FROM travel.execution_steps WHERE plan_id=$1 AND status='pending' ORDER BY created_at LIMIT 1`, plan).Scan(&request.ExecutionStepID, &request.PlanRevision, &request.TaskID, &request.TaskRevision, &request.ItemID, &request.Generation, &request.Domain, &raw, &request.InputHash); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &request.Input)
	request.SchemaVersion = 1
	request.PlanID = plan
	request.Requirements = []v1.Requirement{}
	request.Ranking = f.definition.Tasks[0].Ranking
	var deadline time.Time
	var run string
	if err := f.q.f.Admin.QueryRow(ctx, "SELECT source_run_id,expires_at FROM control.execution_scopes WHERE scope_id=$1", f.ref.ScopeID).Scan(&run, &deadline); err != nil {
		t.Fatal(err)
	}
	request.Scope = v1.RuntimeScope{UserID: "user", MainThreadID: "conversation", RunID: run, ToolCallID: "exec:" + request.ExecutionStepID, OperationID: request.ExecutionStepID, Attempt: 1, Deadline: deadline.UTC().Format(time.RFC3339Nano)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"candidate_id":"hotel-1","title":"真实地点候选","price":null,"attributes":{},"source_url":null,"missing_fields":["price","inventory"]}]}`))
	}))
	defer server.Close()
	provider := queryProvider(server.Client(), server.URL)
	provider.Domain = request.Domain
	if len(expiries) > 0 {
		call := provider.Call
		provider.Call = func(ctx context.Context, args json.RawMessage) (toolquery.Result, error) {
			result, err := call(ctx, args)
			until := time.Now().UTC().Add(expiries[0])
			result.ExpiresAt = &until
			return result, err
		}
	}
	repo := travelpg.New(f.q.f.Travel)
	service, err := toolquery.New(repo, f.q.permits, []toolquery.Provider{provider}, toolquery.Options{CleanupGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(request.Input)
	record, err := service.Execute(ctx, f.ref, toolquery.QueryInput{CallKey: "exec:" + request.ExecutionStepID, Domain: request.Domain, Provider: "fixture", Mode: "live", Arguments: body})
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := repo.LocalOrImport(ctx, f.ref, record.ID, record.ResultRevision)
	if err != nil {
		t.Fatal(err)
	}
	return request, view
}

func TestSelectionAndRefreshExpiredEvidenceView(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	created, err := f.service.Create(ctx, f.ref, "create:first", readyDefinition(f))
	if err != nil {
		t.Fatal(err)
	}
	request, record := preparePlanQuery(t, f, created.PlanID, 2*time.Second)
	if _, err = f.service.AcceptQueryResult(ctx, f.ref, request, record); err != nil {
		t.Fatal(err)
	}
	before, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil || before.Items[0].NeedsQuery {
		t.Fatal("fresh result not recognized", err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, *record.Record.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Until(expiry) + 20*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	after, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil || !after.Items[0].NeedsQuery || after.Items[0].Status != "stale" || after.Snapshot.TaskVersions[0].TaskRevision != 1 {
		t.Fatalf("expired fact still presented current: %+v %v", after.Items, err)
	}
}

func TestSelectionAndRefreshVersions(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	created, err := f.service.Create(ctx, f.ref, "create:first", readyDefinition(f))
	if err != nil {
		t.Fatal(err)
	}
	request, record := preparePlanQuery(t, f, created.PlanID)
	accepted, err := f.service.AcceptQueryResult(ctx, f.ref, request, record)
	if err != nil || accepted.Status != "succeeded" || accepted.Coverage != "partial" || len(accepted.Candidates) != 1 || accepted.Candidates[0].Price != nil {
		t.Fatalf("unknown/partial query fabricated success: %+v %v", accepted, err)
	}
	selection := v1.Selection{SelectionID: "selected-hotel", TaskID: request.TaskID, TaskRevision: request.TaskRevision, ItemID: request.ItemID, Evidence: v1.EvidenceRef{EvidenceID: record.ReferenceID, ResultRevision: record.ResultRevision, CandidateID: "hotel-1"}, Reason: "保留未知报价，未预订"}
	body, _ := json.Marshal(selection)
	commit, err := f.service.ApplyPatch(ctx, f.ref, "selection:first", fixturePlanPatch(t, created.PlanID, 1, `[{"op":"set_selection","value":`+string(body)+`}]`))
	if err != nil || commit.CommittedRevision != 2 || len(commit.InvalidatedTaskIDs) != 0 {
		t.Fatalf("selection changed definition: %+v %v", commit, err)
	}
	refresh, err := f.service.ApplyPatch(ctx, f.ref, "refresh:first", fixturePlanPatch(t, created.PlanID, 2, `[{"op":"refresh_queries","task_id":"task_hotel","item_ids":["item_hotel"],"reason":"主动重查"}]`))
	if err != nil || refresh.Changed || refresh.CommittedRevision != 2 {
		t.Fatalf("refresh created content revision: %+v %v", refresh, err)
	}
	view, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil || !view.Items[0].NeedsQuery || view.Snapshot.TaskVersions[0].TaskRevision != 1 {
		t.Fatalf("refresh changed definition version: %+v %v", view, err)
	}
	var generation int
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT max(generation) FROM travel.execution_steps WHERE plan_id=$1", created.PlanID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("refresh generation mismatch: %d %v", generation, err)
	}
	late, err := f.service.AcceptQueryResult(ctx, f.ref, request, record)
	if err != nil || late.Status != "stale" {
		t.Fatalf("old generation overwrote current query: %+v %v", late, err)
	}
}

func TestSelectionAndRefreshRejectsForgedAndEqualInputOldFacts(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	created, err := f.service.Create(ctx, f.ref, "create:first", readyDefinition(f))
	if err != nil {
		t.Fatal(err)
	}
	request, record := preparePlanQuery(t, f, created.PlanID)
	raw, _ := json.Marshal(record)
	var forged evidence.View
	json.Unmarshal(raw, &forged)
	forged.Record.Snapshot["candidates"] = json.RawMessage(`[{"candidate_id":"hotel-1","title":"伪造零价","price":{"amount_minor":0,"currency":"CNY","basis":"night"},"attributes":{},"source_url":null,"missing_fields":[]}]`)
	if _, err = f.service.AcceptQueryResult(ctx, f.ref, request, forged); err == nil {
		t.Fatal("caller fabricated persisted provider facts")
	}
	altered := request
	altered.InputHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err = f.service.AcceptQueryResult(ctx, f.ref, altered, record); err == nil {
		t.Fatal("forged execution input hash accepted")
	}
	accepted, err := f.service.AcceptQueryResult(ctx, f.ref, request, record)
	if err != nil || accepted.Candidates[0].Price != nil {
		t.Fatal("unknown price changed during acceptance", err)
	}
	_, mutationErr := f.q.f.Travel.Exec(ctx, "UPDATE travel.execution_steps SET result='{}' WHERE result IS NOT NULL")
	var pgErr *pgconn.PgError
	if !errors.As(mutationErr, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("completed result mutation not rejected by fact constraint: %v", mutationErr)
	}
	if _, err = f.service.ApplyPatch(ctx, f.ref, "selection:fake", fixturePlanPatch(t, created.PlanID, 1, `[{"op":"set_selection","value":{"selection_id":"fake","task_id":"task_hotel","task_revision":1,"item_id":"item_hotel","evidence":{"evidence_id":"`+record.ReferenceID+`","result_revision":1,"candidate_id":"invented"},"reason":"fake"}}]`)); err == nil {
		t.Fatal("invented candidate selected")
	}
	changed, err := f.service.ApplyPatch(ctx, f.ref, "patch:definition", fixturePlanPatch(t, created.PlanID, 1, `[{"op":"update_task","task_id":"task_hotel","changes":{"set":{"title":"新任务定义，查询参数相同"}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	next := loadPlanRequest(t, f, created.PlanID)
	if changed.CommittedRevision != 2 || next.TaskRevision != 2 || next.InputHash != request.InputHash {
		t.Fatal("fixture must change definition without changing physical query input")
	}
	if _, err = f.service.AcceptQueryResult(ctx, f.ref, next, record); err == nil {
		t.Fatal("equal input hash bypassed whole-task requery")
	}
	late, err := f.service.AcceptQueryResult(ctx, f.ref, request, record)
	if err != nil || late.Status != "stale" {
		t.Fatalf("old task version rebound: %+v %v", late, err)
	}
}

func TestSelectionAndRefreshTrainUsesFormalInput(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	d := readyDefinition(f)
	d.Tasks[0].TaskID = "task_train"
	d.Tasks[0].Parameters = v1.TaskParameters{Train: v1.Field[v1.TrainParameters]{Present: true, Value: v1.TrainParameters{Origin: v1.Field[string]{Present: true, Value: "北京南"}, Destination: v1.Field[string]{Present: true, Value: "上海虹桥"}, DepartureDate: v1.Field[string]{Present: true, Value: "2026-11-10"}, Passengers: v1.Field[int64]{Present: true, Value: 1}, Keywords: v1.Field[[]string]{Present: true, Value: []string{"高铁"}}}}}
	d.Tasks[0].Items = []v1.QueryItem{{ItemID: "item_train", Domain: "train", DependsOn: []string{}}}
	created, err := f.service.Create(ctx, f.ref, "create:train", d)
	if err != nil {
		t.Fatal(err)
	}
	request := loadPlanRequest(t, f, created.PlanID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error_code":0,"result":[{"train_no":"G1","departure_station":"北京南","arrival_station":"上海虹桥","departure_time":"08:00","arrival_time":"12:00","duration":"04:00","enable_booking":"2","prices":[{"seat_name":"二等座","seat_type_code":"O","price":"553.5","num":"5"}]}]}`))
	}))
	defer server.Close()
	client := train.New("fixture-key", providerhttp.SingleAttempt(&rewriteProvider{target: server.URL, client: server.Client()}), func() time.Time { return time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC) })
	provider := toolquery.TravelProvider("train", "fixture", "live", []string{"query:fixture"}, client.Search)
	repo := travelpg.New(f.q.f.Travel)
	queries, err := toolquery.New(repo, f.q.permits, []toolquery.Provider{provider}, toolquery.Options{CleanupGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(request.Input)
	record, err := queries.Execute(ctx, f.ref, toolquery.QueryInput{CallKey: "exec:" + request.ExecutionStepID, Domain: "train", Provider: "fixture", Mode: "live", Arguments: body})
	if err != nil {
		t.Fatal("formal train input could not reach existing provider", err)
	}
	view, _, err := repo.LocalOrImport(ctx, f.ref, record.ID, record.ResultRevision)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.AcceptQueryResult(ctx, f.ref, request, view)
	if err != nil || result.Status != "succeeded" || len(result.Candidates) != 1 || result.Candidates[0].Price == nil || result.Candidates[0].Price.AmountMinor != 55350 {
		t.Fatalf("real train result lost at execution boundary: %+v %v", result, err)
	}
}

func newPlanFixture(t *testing.T, sourceScopes ...json.RawMessage) planFixture {
	t.Helper()
	q := newQueryFixture(t)
	ctx := context.Background()
	var run string
	if err := q.f.Admin.QueryRow(ctx, "SELECT run_id FROM platform.agent_jobs WHERE id=$1", q.ref.OwnerID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	tx, err := q.f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	sourceScope := json.RawMessage(`{}`)
	if len(sourceScopes) > 0 {
		sourceScope = sourceScopes[0]
	}
	scope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: "plan-scope", OwnerService: "travel", SubjectID: "user", SourceRunID: run, ConversationID: "conversation", DelegationID: "plan-delegation", Capabilities: []string{"commit", "evidence", "tool", "model", "execute", "memory"}, Targets: []string{"plan", "hotel", "train", "weather", "web", "policy", "model:fixture", "memory:travel"}, SourceScope: sourceScope, ContextEpoch: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.f.Travel.Exec(ctx, `INSERT INTO travel.threads(id,user_id,conversation_id,origin_thread_id,last_main_context_epoch) VALUES('plan-thread','user','conversation','conversation',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.f.Travel.Exec(ctx, `INSERT INTO travel.tasks(id,caller_id,subject_id,conversation_id,source_run_id,delegation_id,request_key,input_hash,arguments,scope_id,scope_hash,thread_id,status,worker_id,lease_epoch,lease_expires_at,deadline_at) VALUES('plan-task','platform','user','conversation',$1,'plan-delegation','request','hash','{}',$2,$3,'plan-thread','running','plan-worker',1,clock_timestamp()+interval '20 seconds',clock_timestamp()+interval '1 minute')`, run, scope.ScopeID, scope.ScopeHash); err != nil {
		t.Fatal(err)
	}
	ref := v1.ExecRef{ScopeID: scope.ScopeID, ScopeHash: scope.ScopeHash, OwnerKind: "task", OwnerID: "plan-task", ThreadID: "plan-thread", Lease: v1.LeaseRef{WorkerID: "plan-worker", Epoch: 1, ExpiresAt: time.Now().Add(20 * time.Second)}}
	raw, err := os.ReadFile("../../../openspec/changes/refactor-agent-kernel-plan-execute/contracts/examples/15-minimal-plan-missing-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition v1.PlanDefinition
	if err = runtimeprotocol.DecodeStrict(raw, "PlanDefinition", &definition); err != nil {
		t.Fatal(err)
	}
	// Requirements with real sources are exercised separately. This fixture is
	// the formal minimal missing-input case, without any manufactured provenance.
	definition.Requirements = []v1.Requirement{}
	definition.Tasks[0].RequirementIDs = []string{}
	return planFixture{q, ref, travelplan.New(travelpg.New(q.f.Travel)), definition}
}

func seedPlanMemory(t *testing.T, f planFixture, revision int64, status string) {
	t.Helper()
	ctx := context.Background()
	operation := "memory-first"
	if revision == 2 {
		operation = "memory-retract"
	}
	if _, err := f.q.f.Platform.Exec(ctx, `INSERT INTO platform.memory_operations(id,user_id,conversation_id,thread_id,context_epoch,kind,request_hash,arguments,base_memory_revision,status,phase) VALUES($1,'user','conversation','conversation',1,'explicit_write','hash','{}',$2,'succeeded','complete')`, operation, revision-1); err != nil {
		t.Fatal(err)
	}
	snapshot := v1.UserMemorySnapshot{SchemaVersion: 1, Memories: []v1.MemoryRecord{{MemoryID: "memory-hotel", Revision: revision, Domain: "travel", Text: "酒店靠近地铁", Authority: "explicit", Status: status, Sources: []v1.SourceRef{{Kind: "message", ID: "source-user-message"}}}}}
	body, _ := json.Marshal(snapshot)
	if _, err := f.q.f.Platform.Exec(ctx, `INSERT INTO platform.user_memory_revisions(user_id,revision,schema_version,base_revision,operation_id,snapshot) VALUES('user',$1,1,$2,$3,$4::jsonb)`, revision, revision-1, operation, body); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownPriceAndProvenance(t *testing.T) {
	f := newPlanFixture(t, json.RawMessage(`{"memory_refs":[{"memory_id":"memory-hotel","revision":1}]}`))
	ctx := context.Background()
	seedPlanMemory(t, f, 1, "active")
	d := readyDefinition(f)
	d.Requirements = []v1.Requirement{{RequirementID: "memory-preference", Text: "酒店靠近地铁", Origin: "memory", Scope: "tasks", TaskIDs: []string{"task_hotel"}, Sources: []v1.SourceRef{{Kind: "memory", ID: "memory-hotel", Revision: v1.Field[int64]{Present: true, Value: 1}}}}}
	d.Tasks[0].RequirementIDs = []string{"memory-preference"}
	created, err := f.service.Create(ctx, f.ref, "create:first", d)
	if err != nil {
		t.Fatal("authorized memory requirement rejected", err)
	}
	seedPlanMemory(t, f, 2, "retracted")
	if _, err = f.service.ApplyPatch(ctx, f.ref, "patch:retracted", fixturePlanPatch(t, created.PlanID, 1, `[{"op":"update_plan","changes":{"set":{"title":"changed"}}}]`)); err == nil {
		t.Fatal("withdrawn memory still authorized plan change")
	}
	var current int
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT current_revision FROM travel.plans WHERE id=$1", created.PlanID).Scan(&current); err != nil || current != 1 {
		t.Fatalf("withdrawn source partially committed: %d %v", current, err)
	}
	var canRead bool
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT has_table_privilege($1,'platform.user_memory_revisions','SELECT')", f.q.f.Principals.Runtime("travel")).Scan(&canRead); err != nil || canRead {
		t.Fatal("memory projection granted direct business access")
	}
}

func TestUnknownPriceMemoryProjectionRevocationRace(t *testing.T) {
	for _, readFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "read_first", false: "retract_first"}[readFirst], func(t *testing.T) {
			f := newPlanFixture(t, json.RawMessage(`{"memory_refs":[{"memory_id":"memory-hotel","revision":1}]}`))
			ctx := context.Background()
			seedPlanMemory(t, f, 1, "active")
			read, err := f.q.f.Travel.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Rollback(ctx)
			retract, err := f.q.f.Platform.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retract.Rollback(ctx)
			projection := func() ([]v1.MemoryRecord, error) {
				var raw []byte
				err := read.QueryRow(ctx, "SELECT control.read_related_memory($1,$2,'travel')", f.ref.ScopeID, f.ref.ScopeHash).Scan(&raw)
				var entries []v1.MemoryRecord
				if err == nil {
					err = json.Unmarshal(raw, &entries)
				}
				return entries, err
			}
			lockUser := func() error {
				_, err := retract.Exec(ctx, "UPDATE platform.users SET username=username WHERE id='user'")
				return err
			}
			if readFirst {
				entries, err := projection()
				if err != nil || len(entries) != 1 {
					t.Fatalf("active source unavailable: %v %v", entries, err)
				}
				done := make(chan error, 1)
				go func() { done <- lockUser() }()
				awaitBlocked(t, f.q.f.Admin, int32(retract.Conn().PgConn().PID()))
				if err = read.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			} else {
				if err = lockUser(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = retract.Exec(ctx, `INSERT INTO platform.memory_operations(id,user_id,conversation_id,thread_id,context_epoch,kind,request_hash,arguments,base_memory_revision,status,phase) VALUES('memory-retract','user','conversation','conversation',1,'explicit_write','hash','{}',1,'succeeded','complete');INSERT INTO platform.user_memory_revisions(user_id,revision,schema_version,base_revision,operation_id,snapshot) VALUES('user',2,1,1,'memory-retract','{"schema_version":1,"memories":[{"memory_id":"memory-hotel","revision":2,"domain":"travel","text":"撤回","authority":"explicit","status":"retracted","sources":[]}]}')`); err != nil {
				t.Fatal(err)
			}
			if readFirst {
				if err = retract.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				type outcome struct {
					entries []v1.MemoryRecord
					err     error
				}
				done := make(chan outcome, 1)
				go func() { entries, err := projection(); done <- outcome{entries, err} }()
				awaitBlocked(t, f.q.f.Admin, int32(read.Conn().PgConn().PID()))
				if err = retract.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				result := <-done
				if result.err != nil || len(result.entries) != 0 {
					t.Fatalf("withdrawn source survived lock wait: %+v", result)
				}
				if err = read.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var raw []byte
			if err = f.q.f.Travel.QueryRow(ctx, "SELECT control.read_related_memory($1,$2,'travel')", f.ref.ScopeID, f.ref.ScopeHash).Scan(&raw); err != nil || string(raw) != "[]" {
				t.Fatalf("stale source projection returned: %s %v", raw, err)
			}
			denied(t, f.q.f.Travel, "SELECT platform.private_memory_projection('{}','travel')")
			denied(t, f.q.f.Platform, "SELECT platform.private_memory_projection('{}','travel')")
		})
	}
}

func TestUnknownPricePolicySourceAndEscalation(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "policy-source-fixture", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "query_policy_evidence"}, func(context.Context, *sdk.CallToolRequest, map[string]any) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{StructuredContent: map[string]any{"schema_version": 1, "status": "ok", "collection": "fixture", "fragments": []any{map[string]any{"id": "policy-hotel", "chunk_id": "policy-hotel", "content": "酒店每晚额度供模型规划取舍，不作程序资格过滤", "metadata": map[string]any{"source": "policy.pdf"}}}}}, nil, nil
	})
	receiver := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	defer receiver.Close()
	client, err := mcp.NewHTTPClient(mcp.HTTPConfig{Endpoint: receiver.URL, ServiceID: "travel", BearerToken: "fixture-token", Collections: []string{"fixture"}, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	provider := toolquery.PolicyProvider("fixture", []string{"query:fixture"}, client.CallEvidence)
	queries, err := toolquery.New(travelpg.New(f.q.f.Travel), f.q.permits, []toolquery.Provider{provider}, toolquery.Options{CleanupGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queries.Execute(ctx, f.ref, toolquery.QueryInput{CallKey: "policy:first", Domain: "policy", Provider: "fixture", Mode: "live", Arguments: json.RawMessage(`{"query":"酒店政策","collection":"fixture"}`)}); err != nil {
		t.Fatal(err)
	}
	d := readyDefinition(f)
	d.Requirements = []v1.Requirement{{RequirementID: "policy-requirement", Text: "酒店额度以适用政策为依据", Origin: "policy", Scope: "tasks", TaskIDs: []string{"task_hotel"}, Sources: []v1.SourceRef{{Kind: "policy", ID: "policy-hotel", Revision: v1.Field[int64]{Present: true, Value: 1}}}}}
	d.Tasks[0].RequirementIDs = []string{"policy-requirement"}
	created, err := f.service.Create(ctx, f.ref, "create:first", d)
	if err != nil {
		t.Fatal("real retrieved policy reference rejected", err)
	}
	forged := fixturePlanPatch(t, created.PlanID, 1, `[{"op":"update_requirement","requirement_id":"policy-requirement","changes":{"set":{"sources":[{"kind":"policy","id":"invented-policy","revision":1}]}}}]`)
	if _, err = f.service.ApplyPatch(ctx, f.ref, "patch:forged", forged); err == nil {
		t.Fatal("invented policy source accepted")
	}
}

func TestUnknownPriceImportedPolicyProvenance(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"raw":{"schema_version":1,"status":"ok","collection":"fixture","fragments":[{"id":"policy-prior","chunk_id":"policy-prior","content":"保存的适用政策","metadata":{"source":"policy.pdf"}}]}}`))
	}))
	defer receiver.Close()
	provider := queryProvider(receiver.Client(), receiver.URL)
	provider.Domain = "policy"
	record, err := queryService(t, f.q, provider).Execute(ctx, f.q.ref, toolquery.QueryInput{CallKey: "policy:prior", Domain: "policy", Provider: "fixture", Mode: "live", Arguments: json.RawMessage(`{"query":"酒店政策","collection":"fixture"}`)})
	if err != nil {
		t.Fatal(err)
	}
	reader := importPlanEvidence(t, &f, record)
	service := travelplan.NewWithEvidence(travelpg.New(f.q.f.Travel), reader)
	d := readyDefinition(f)
	d.Requirements = []v1.Requirement{{RequirementID: "policy-prior-requirement", Text: "使用已有适用政策", Origin: "policy", Scope: "tasks", TaskIDs: []string{"task_hotel"}, Sources: []v1.SourceRef{{Kind: "policy", ID: "policy-prior", Revision: v1.Field[int64]{Present: true, Value: 1}}}}}
	d.Tasks[0].RequirementIDs = []string{"policy-prior-requirement"}
	if _, err = service.Create(ctx, f.ref, "create:prior-policy", d); err != nil {
		t.Fatal("verified imported policy source rejected", err)
	}
}

func importPlanEvidence(t *testing.T, f *planFixture, record v1.SharedToolEvidenceRecord) *evidence.Reader {
	t.Helper()
	ctx := context.Background()
	body, _ := json.Marshal(record)
	hash, err := evidence.ContentHash(body)
	if err != nil {
		t.Fatal(err)
	}
	locator := evidence.Locator{OriginService: "platform", OriginID: record.ID, OriginRevision: record.ResultRevision, ContentHash: hash}
	manifest, _ := json.Marshal(map[string]any{"evidence": []evidence.Locator{locator}})
	tx, err := f.q.f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: "plan-source-scope", OwnerService: "travel", SubjectID: "user", SourceRunID: recordRunID(t, f.q), ConversationID: "conversation", DelegationID: "plan-delegation", Capabilities: []string{"commit", "evidence", "tool", "model", "execute"}, Targets: []string{"plan", "hotel", "train", "weather", "web", "policy"}, SourceScope: manifest, ContextEpoch: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.f.Travel.Exec(ctx, "UPDATE travel.tasks SET scope_id=$1,scope_hash=$2 WHERE id='plan-task'", scope.ScopeID, scope.ScopeHash); err != nil {
		t.Fatal(err)
	}
	f.ref.ScopeID, f.ref.ScopeHash = scope.ScopeID, scope.ScopeHash
	endpoint, err := handler.NewInternalEvidence(f.q.repo, []handler.ServiceCredential{{ServiceID: "travel", Token: "fixture-policy-transfer"}})
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(endpoint)
	t.Cleanup(origin.Close)
	reader, err := evidence.NewReader(travelpg.New(f.q.f.Travel), []evidence.Remote{{ServiceID: "platform", Slug: "platform", Endpoint: origin.URL, BearerToken: "fixture-policy-transfer"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Import(ctx, f.ref, locator); err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestUnknownPriceUserAndSupplementalEvidenceSources(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	if _, err := f.q.f.Travel.Exec(ctx, `INSERT INTO travel.messages(id,task_id,user_id,conversation_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_key) VALUES('user-source','plan-task','user','conversation','plan-thread',1,'user','安排上海酒店，参考天气资料','task_input',1,'input')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"temperature_c":12}`))
	}))
	defer server.Close()
	queries, err := toolquery.New(travelpg.New(f.q.f.Travel), f.q.permits, []toolquery.Provider{queryProvider(server.Client(), server.URL)}, toolquery.Options{CleanupGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	record, err := queries.Execute(ctx, f.ref, queryInput())
	if err != nil {
		t.Fatal(err)
	}
	d := readyDefinition(f)
	d.Requirements = []v1.Requirement{{RequirementID: "user-requirement", Text: "用户安排，附真实天气资料", Origin: "user", Scope: "tasks", TaskIDs: []string{"task_hotel"}, Sources: []v1.SourceRef{{Kind: "message", ID: "user-source"}, {Kind: "evidence", ID: record.ID, Revision: v1.Field[int64]{Present: true, Value: record.ResultRevision}}}}}
	d.Tasks[0].RequirementIDs = []string{"user-requirement"}
	if _, err = f.service.Create(ctx, f.ref, "create:sources", d); err != nil {
		t.Fatal("real supplemental reference rejected", err)
	}
}

func TestUnknownPriceCrossServiceReuseAndRevocation(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	definition := readyDefinition(f)
	var sourceID string
	if err := f.q.f.Admin.QueryRow(ctx, "SELECT id FROM platform.messages WHERE message_kind='user_request' LIMIT 1").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"candidate_id":"hotel-prior","title":"已查询地点","price":null,"attributes":{},"source_url":null,"missing_fields":["price"]}]}`))
	}))
	defer receiver.Close()
	provider := queryProvider(receiver.Client(), receiver.URL)
	provider.Domain = "hotel"
	args, _ := json.Marshal(definition.Tasks[0].Parameters.Hotel.Value)
	prior, err := queryService(t, f.q, provider).Execute(ctx, f.q.ref, toolquery.QueryInput{CallKey: "hotel:prior", Domain: "hotel", Mode: "live", Provider: "fixture", Arguments: args, SourceRefs: []v1.SourceRef{{Kind: "message", ID: sourceID}}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(prior)
	hash, err := evidence.ContentHash(body)
	if err != nil {
		t.Fatal(err)
	}
	locator := evidence.Locator{OriginService: "platform", OriginID: prior.ID, OriginRevision: 1, ContentHash: hash}
	manifest, _ := json.Marshal(map[string]any{"evidence": []evidence.Locator{locator}})
	tx, err := f.q.f.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope, err := (control.Client{}).IssueScope(ctx, tx, control.ScopeInput{ScopeID: "plan-with-evidence", OwnerService: "travel", SubjectID: "user", SourceRunID: recordRunID(t, f.q), ConversationID: "conversation", DelegationID: "plan-delegation", Capabilities: []string{"commit", "evidence", "tool", "model", "execute"}, Targets: []string{"plan", "hotel", "train", "weather", "web", "policy"}, SourceScope: manifest, ContextEpoch: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.f.Travel.Exec(ctx, "UPDATE travel.tasks SET scope_id=$1,scope_hash=$2 WHERE id='plan-task'", scope.ScopeID, scope.ScopeHash); err != nil {
		t.Fatal(err)
	}
	f.ref.ScopeID, f.ref.ScopeHash = scope.ScopeID, scope.ScopeHash
	endpoint, err := handler.NewInternalEvidence(f.q.repo, []handler.ServiceCredential{{ServiceID: "travel", Token: "fixture-transfer"}})
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(endpoint)
	defer origin.Close()
	repo := travelpg.New(f.q.f.Travel)
	reader, err := evidence.NewReader(repo, []evidence.Remote{{ServiceID: "platform", Slug: "platform", Endpoint: origin.URL, BearerToken: "fixture-transfer"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := reader.Import(ctx, f.ref, locator)
	if err != nil {
		t.Fatal(err)
	}
	service := travelplan.NewWithEvidence(repo, reader)
	created, err := service.Create(ctx, f.ref, "create:reused", definition)
	if err != nil {
		t.Fatal(err)
	}
	request := loadPlanRequest(t, f, created.PlanID)
	result, err := service.AcceptQueryResult(ctx, f.ref, request, imported)
	if err != nil || result.Status != "succeeded" || len(result.Candidates) != 1 || result.Candidates[0].Price != nil {
		t.Fatalf("verified imported fact not accepted: %+v %v", result, err)
	}
	var localCalls int
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT count(*) FROM travel.tool_calls").Scan(&localCalls); err != nil || localCalls != 0 {
		t.Fatal("reuse fabricated a local provider call")
	}
	selection := v1.Selection{SelectionID: "remote-selection", TaskID: request.TaskID, TaskRevision: 1, ItemID: request.ItemID, Evidence: v1.EvidenceRef{EvidenceID: imported.ReferenceID, ResultRevision: 1, CandidateID: "hotel-prior"}, Reason: "候选仍无报价"}
	selected, _ := json.Marshal(selection)
	selectPatch := fixturePlanPatch(t, created.PlanID, 1, `[{"op":"set_selection","value":`+string(selected)+`}]`)
	commit, err := service.ApplyPatch(ctx, f.ref, "selection:prior", selectPatch)
	if err != nil || commit.CommittedRevision != 2 {
		t.Fatal("verified remote selection failed", err)
	}
	if _, err = f.q.f.Admin.Exec(ctx, "DELETE FROM platform.messages WHERE id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AcceptQueryResult(ctx, f.ref, request, imported); err == nil {
		t.Fatal("cached caller-supplied view bypassed current remote source revocation")
	}
	if _, err = service.ApplyPatch(ctx, f.ref, "patch:withdrawn", fixturePlanPatch(t, created.PlanID, 2, `[{"op":"update_plan","changes":{"set":{"title":"after withdrawal"}}}]`)); err == nil {
		t.Fatal("retained remote selection bypassed source revalidation")
	}
	replayed, err := service.ApplyPatch(ctx, f.ref, "selection:prior", selectPatch)
	if err != nil || replayed.CommittedRevision != 2 {
		t.Fatalf("committed receipt lost priority over stale source: %+v %v", replayed, err)
	}
}

func loadPlanRequest(t *testing.T, f planFixture, plan string) v1.ExecutionRequest {
	t.Helper()
	ctx := context.Background()
	var request v1.ExecutionRequest
	var raw []byte
	if err := f.q.f.Admin.QueryRow(ctx, `SELECT id,plan_revision,task_id,task_revision,item_id,generation,domain,input,input_hash FROM travel.execution_steps WHERE plan_id=$1 AND status='pending' ORDER BY created_at LIMIT 1`, plan).Scan(&request.ExecutionStepID, &request.PlanRevision, &request.TaskID, &request.TaskRevision, &request.ItemID, &request.Generation, &request.Domain, &raw, &request.InputHash); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &request.Input)
	request.SchemaVersion = 1
	request.PlanID = plan
	request.Requirements = []v1.Requirement{}
	request.Ranking = f.definition.Tasks[0].Ranking
	var deadline time.Time
	var run string
	if err := f.q.f.Admin.QueryRow(ctx, "SELECT source_run_id,expires_at FROM control.execution_scopes WHERE scope_id=$1", f.ref.ScopeID).Scan(&run, &deadline); err != nil {
		t.Fatal(err)
	}
	request.Scope = v1.RuntimeScope{UserID: "user", MainThreadID: "conversation", RunID: run, ToolCallID: "exec:" + request.ExecutionStepID, OperationID: request.ExecutionStepID, Attempt: 1, Deadline: deadline.UTC().Format(time.RFC3339Nano)}
	return request
}

func TestCommittedActionReplay(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	created, err := f.service.Create(ctx, f.ref, "create:first", f.definition)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.Create(ctx, f.ref, "create:first", f.definition)
	if err != nil || replay.PlanID != created.PlanID || replay.CommittedRevision != 1 {
		t.Fatalf("create replay changed identity: %+v %v", replay, err)
	}
	different := f.definition
	different.Title = "different definition"
	if _, err = f.service.Create(ctx, f.ref, "create:first", different); err == nil {
		t.Fatal("same operation changed payload")
	}
	view, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 || !view.Items[0].NeedsQuery || view.Items[0].Status != "blocked" {
		t.Fatalf("missing query inputs pretended executable: %+v", view.Items)
	}
	patch := v1.PlanPatch{SchemaVersion: 1, PlanID: created.PlanID, ExpectedRevision: 1, Reason: "title amendment", Operations: []v1.PatchOperation{{Op: "update_plan", Changes: v1.Field[v1.FieldChanges]{Present: true, Value: v1.FieldChanges{Set: v1.Field[map[string]json.RawMessage]{Present: true, Value: map[string]json.RawMessage{"title": json.RawMessage(`"amended"`)}}}}}}}
	committed, err := f.service.ApplyPatch(ctx, f.ref, "patch:first", patch)
	if err != nil || committed.CommittedRevision != 2 {
		t.Fatalf("patch not committed: %+v %v", committed, err)
	}
	replay, err = f.service.ApplyPatch(ctx, f.ref, "patch:first", patch)
	if err != nil || replay.CommittedRevision != 2 {
		t.Fatalf("old-expected-version replay was not original receipt: %+v %v", replay, err)
	}
	var plans, revisions, actions int
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel.plans),(SELECT count(*) FROM travel.revisions),(SELECT count(*) FROM travel.actions)").Scan(&plans, &revisions, &actions); err != nil || plans != 1 || revisions != 2 || actions != 2 {
		t.Fatalf("replay multiplied durable facts: %d/%d/%d %v", plans, revisions, actions, err)
	}
}

func TestPatchAtomicDatabaseRollback(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	if _, err := f.q.f.Admin.Exec(ctx, `CREATE FUNCTION travel.fail_plan_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture execution arrangement failed'; END $$; CREATE TRIGGER fail_plan_commit BEFORE INSERT ON travel.execution_steps FOR EACH ROW EXECUTE FUNCTION travel.fail_plan_commit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(ctx, f.ref, "create:first", f.definition); err == nil {
		t.Fatal("partial transaction reported success")
	}
	var plans, revisions, actions int
	if err := f.q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel.plans),(SELECT count(*) FROM travel.revisions),(SELECT count(*) FROM travel.actions)").Scan(&plans, &revisions, &actions); err != nil || plans != 0 || revisions != 0 || actions != 0 {
		t.Fatalf("failed transaction retained pointer/receipt: %d/%d/%d %v", plans, revisions, actions, err)
	}
}

func TestPatchAtomicConcurrentCAS(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	created, err := f.service.Create(ctx, f.ref, "create:first", readyDefinition(f))
	if err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for i := range 2 {
		go func(index int) {
			start.Wait()
			patch := fixturePlanPatch(t, created.PlanID, 1, fmt.Sprintf(`[{"op":"update_plan","changes":{"set":{"title":"writer-%d"}}}]`, index))
			_, err := f.service.ApplyPatch(ctx, f.ref, fmt.Sprintf("patch:%d", index), patch)
			outcomes <- err
		}(i)
	}
	start.Done()
	wins, conflicts := 0, 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			wins++
		} else if errors.Is(err, model.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS writers: %d/%d", wins, conflicts)
	}
	var revisions, actions, current int
	if err = f.q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel.revisions),(SELECT count(*) FROM travel.actions),(SELECT current_revision FROM travel.plans WHERE id=$1)", created.PlanID).Scan(&revisions, &actions, &current); err != nil || revisions != 2 || actions != 2 || current != 2 {
		t.Fatalf("CAS partially committed: %d/%d/%d %v", revisions, actions, current, err)
	}
}

func TestPatchAtomicExpiryAfterStorage(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	if _, err := f.q.f.Admin.Exec(ctx, `CREATE FUNCTION travel.expire_plan_owner() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE travel.tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=NEW.owner_task_id; RETURN NEW; END $$;CREATE TRIGGER expire_plan_owner BEFORE INSERT ON travel.execution_steps FOR EACH ROW EXECUTE FUNCTION travel.expire_plan_owner()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(ctx, f.ref, "create:first", readyDefinition(f)); err == nil {
		t.Fatal("owner expired after storage but commit succeeded")
	}
	var plans, revisions, steps, actions int
	if err := f.q.f.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel.plans),(SELECT count(*) FROM travel.revisions),(SELECT count(*) FROM travel.execution_steps),(SELECT count(*) FROM travel.actions)").Scan(&plans, &revisions, &steps, &actions); err != nil || plans+revisions+steps+actions != 0 {
		t.Fatalf("expired owner left domain writes: %d/%d/%d/%d %v", plans, revisions, steps, actions, err)
	}
}

func TestCommittedActionMulticityFixture(t *testing.T) {
	f := newPlanFixture(t)
	ctx := context.Background()
	raw, err := os.ReadFile("../../../openspec/changes/refactor-agent-kernel-plan-execute/contracts/examples/26-multicity-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var source v1.PlanSnapshot
	if err = runtimeprotocol.DecodeStrict(raw, "PlanSnapshot", &source); err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.f.Travel.Exec(ctx, `INSERT INTO travel.messages(id,task_id,user_id,conversation_id,thread_id,ordinal,role,content,message_kind,context_epoch,source_key) VALUES('msg_multicity_demo','plan-task','user','conversation','plan-thread',1,'user','北京出发，经上海杭州返回北京，预算3000元','task_input',1,'task-input')`); err != nil {
		t.Fatal(err)
	}
	created, err := f.service.Create(ctx, f.ref, "create:multicity", source.Definition)
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.service.View(ctx, f.ref, created.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Snapshot.Definition.Stops) != 4 || len(view.Snapshot.Definition.Legs) != 3 || len(view.Snapshot.Definition.Tasks) != len(source.Definition.Tasks) {
		t.Fatal("multicity/return associations changed")
	}
	expected := 0
	for _, task := range source.Definition.Tasks {
		expected += len(task.Items)
	}
	if len(view.Items) != expected {
		t.Fatal("multicity query arrangements lost")
	}
}
