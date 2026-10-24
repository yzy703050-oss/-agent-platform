package integration

import (
	"agent-platform/backend/internal/client/demo"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-platform/backend/internal/model"
	pg "agent-platform/backend/internal/repository/postgres"
	tripsvc "agent-platform/backend/internal/service/trip"
	"agent-platform/backend/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func travelFixture(t *testing.T) (*fixture, model.TravelExecution, *tripsvc.Service) {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "UPDATE conversations SET agent_type='travel' WHERE id=$1", f.conversation); err != nil {
		t.Fatal(err)
	}
	r := f.admit(t)
	if _, err := f.repo.TransitionRun(ctx, model.TransitionRunInput{Scope: f.scope, RunID: r.ID, Attempt: r.Attempt, From: "accepted", To: "running"}); err != nil {
		t.Fatal(err)
	}
	return f, model.TravelExecution{Scope: f.scope, ConversationID: f.conversation, RunID: r.ID, Attempt: r.Attempt}, tripsvc.New(f.repo, time.Now)
}

type travelQueryProbe struct {
	calls            atomic.Int32
	entered, release chan struct{}
}
type travelQueryFunc func(context.Context, model.TravelQuery) (model.TravelEvidence, error)

func (f travelQueryFunc) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	return f(ctx, q)
}

func (p *travelQueryProbe) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	p.calls.Add(1)
	if p.entered != nil {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return model.TravelEvidence{}, ctx.Err()
		}
	}
	price := int64(10000)
	return model.TravelEvidence{Status: "ok", Source: model.TravelSource{Provider: "test", URL: "https://example.test/train", FetchedAt: time.Now()}, Candidates: []model.TravelCandidate{{ID: "G1", Name: "G1", Origin: q.Origin, Destination: q.Destination, PriceFen: &price}}}, nil
}

func TestTravelQueries(t *testing.T) {
	ctx := context.Background()
	f, ref, service := travelFixture(t)
	p, err := service.Apply(ctx, ref, "query-plan", travelCommand())
	if err != nil {
		t.Fatal(err)
	}
	provider := &travelQueryProbe{}
	queries := tripsvc.NewWithQueries(f.repo, provider, time.Now)
	q := model.TravelQuery{Domain: "train", Origin: " 上海 ", Destination: "杭州", Date: "2026-10-25"}
	first, err := queries.Query(ctx, ref, q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Query.Origin != "上海" {
		t.Fatal("query was not normalized")
	}
	q.PlanID = p.Plan.ID
	q.PlanRevision = 1
	q.TaskID = "t1"
	bound, err := queries.Query(ctx, ref, q)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 1 || bound.PlanID != p.Plan.ID || bound.ID == first.ID {
		t.Fatalf("did not reuse and rebind scoped evidence: %+v calls=%d", bound, provider.calls.Load())
	}
	selected, err := service.Apply(ctx, ref, "select", model.TripCommand{Operation: "select", PlanID: p.Plan.ID, ExpectedRevision: 1, Selections: []model.TripSelection{{TaskID: "t1", Domain: "train", Ref: model.TravelCandidateRef{QueryID: bound.ID, ResultRevision: bound.ResultRevision, CandidateID: "G1"}}}})
	if err != nil || selected.Plan.Tasks[0].Train == nil {
		t.Fatalf("evidence selection: %+v %v", selected, err)
	}
	// A query begun against an old plan revision must not bind its late result.
	blocked := &travelQueryProbe{entered: make(chan struct{}), release: make(chan struct{})}
	lateQueries := tripsvc.NewWithQueries(f.repo, blocked, time.Now)
	if _, err = f.pool.Exec(ctx, "UPDATE travel_query_evidence SET fetched_at=now()-interval '10 minutes',expires_at=now()-interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	inspection, err := service.Inspect(ctx, ref, p.Plan.ID)
	if err != nil || inspection.Plan.Tasks[0].Train != nil {
		t.Fatalf("expired stored selection remained current: %+v %v", inspection, err)
	}
	expired := false
	for _, issue := range inspection.Validation.Issues {
		expired = expired || issue.Code == "selection_expired"
	}
	if !expired {
		t.Fatal("stored evidence expiry was not reported")
	}
	lateQuery := model.TravelQuery{Domain: "train", PlanID: p.Plan.ID, PlanRevision: 2, TaskID: "t1", Origin: "上海", Destination: "杭州", Date: "2026-10-25"}
	done := make(chan error, 1)
	go func() { _, err := lateQueries.Query(ctx, ref, lateQuery); done <- err }()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	date := "2026-10-27"
	_, err = service.Apply(ctx, ref, "change-while-querying", model.TripCommand{Operation: "amend", PlanID: p.Plan.ID, ExpectedRevision: 2, Changes: []model.TripTaskChange{{TaskID: "t1", DepartureDate: &date}}})
	if err != nil {
		t.Fatal(err)
	}
	close(blocked.release)
	if err = <-done; !errors.Is(err, model.ErrConflict) {
		t.Fatalf("accepted stale query result: %v", err)
	}
	for _, kind := range []string{"transient", "exhausted", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			client := travelQueryFunc(func(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
				n := calls.Add(1)
				if kind == "malformed" {
					return model.TravelEvidence{}, errors.New("invalid external payload")
				}
				if n == 1 || kind == "exhausted" {
					return model.TravelEvidence{}, model.ErrProviderTransient
				}
				return (&travelQueryProbe{}).Search(ctx, q)
			})
			query := model.TravelQuery{Domain: "train", Origin: "南京", Destination: "杭州", Date: "2026-10-28"}
			if kind == "exhausted" {
				query.Date = "2026-10-29"
			}
			if kind == "malformed" {
				query.Date = "2026-10-30"
			}
			result, err := tripsvc.NewWithQueries(f.repo, client, time.Now).Query(ctx, ref, query)
			if err != nil {
				t.Fatal(err)
			}
			// Provider Client owns source retries. Service must not multiply
			// those attempts or turn a failed provider call into candidate facts.
			if result.Status != "error" || calls.Load() != 1 || len(result.Candidates) > 0 {
				t.Fatalf("Service retried the Client or fabricated results: %+v calls=%d", result, calls.Load())
			}
		})
	}
}

func TestTravelDemoQueries(t *testing.T) {
	ctx := context.Background()
	f, ref, service := travelFixture(t)
	p, err := service.Apply(ctx, ref, "demo-plan", travelCommand())
	if err != nil {
		t.Fatal(err)
	}
	demoQueries := tripsvc.NewWithQueries(f.repo, demo.New(time.Now), time.Now).WithQueryNamespace("demo")
	q := model.TravelQuery{Domain: "train", Origin: "上海", Destination: "杭州", Date: "2026-10-25", PlanID: p.Plan.ID, PlanRevision: 1, TaskID: "t1"}
	e, err := demoQueries.Query(ctx, ref, q)
	if err != nil || !e.IsDemo || len(e.Candidates) == 0 || !e.Candidates[0].IsDemo {
		t.Fatalf("unlabelled demo: %+v %v", e, err)
	}
	saved, err := service.Apply(ctx, ref, "demo-select", model.TripCommand{Operation: "select", PlanID: p.Plan.ID, ExpectedRevision: 1, Selections: []model.TripSelection{{TaskID: "t1", Domain: "train", Ref: model.TravelCandidateRef{QueryID: e.ID, ResultRevision: 1, CandidateID: e.Candidates[0].ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Read(ctx, ref.Scope, saved.Plan.ID)
	if err != nil || loaded.Tasks[0].Train == nil || !loaded.Tasks[0].Train.Candidate.IsDemo {
		t.Fatalf("saved demo label lost: %v", err)
	}
	live := &travelQueryProbe{}
	q.PlanRevision = 2
	real, err := tripsvc.NewWithQueries(f.repo, live, time.Now).WithQueryNamespace("live").Query(ctx, ref, q)
	if err != nil || real.IsDemo || live.calls.Load() != 1 || real.ID == e.ID {
		t.Fatal("demo cache leaked into live mode", err)
	}
	hotel, err := demoQueries.Query(ctx, ref, model.TravelQuery{Domain: "hotel", City: "杭州"})
	if err != nil || !hotel.IsDemo || hotel.Candidates[0].PriceFen != nil || hotel.Candidates[0].Remaining != nil {
		t.Fatal("demo hotel invented price/inventory", err)
	}
	weather, err := demoQueries.Query(ctx, ref, model.TravelQuery{Domain: "weather", City: "杭州", Date: "2026-10-25"})
	if err != nil || !weather.IsDemo || len(weather.Weather) != 1 {
		t.Fatal("missing labelled demo weather", err)
	}
}
func travelCommand() model.TripCommand {
	oneWay := false
	return model.TripCommand{Operation: "propose", RoundTrip: &oneWay, Tasks: []model.TripTask{{Origin: "上海", Destination: "杭州", DepartureDate: "2026-10-25"}}}
}

func TestTravelTransactions(t *testing.T) {
	ctx := context.Background()
	t.Run("upgrade_and_rollback", func(t *testing.T) {
		f := setupMigrations(t, func(ctx context.Context, pool *pgxpool.Pool) error {
			if _, err := pool.Exec(ctx, "CREATE TABLE schema_migrations(version text PRIMARY KEY)"); err != nil {
				return err
			}
			for _, migration := range []struct{ version, file string }{{"001", "001_platform.up.sql"}, {"002", "002_message_delivery.up.sql"}, {"003", "003_run_output.up.sql"}} {
				data, err := migrations.Files.ReadFile(migration.file)
				if err != nil {
					return err
				}
				if _, err = pool.Exec(ctx, string(data)); err != nil {
					return err
				}
				if _, err = pool.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", migration.version); err != nil {
					return err
				}
			}
			return nil
		})
		// A deliberate name collision fails 004 after earlier CREATE statements.
		if _, err := f.pool.Exec(ctx, "CREATE TABLE travel_decisions(collision integer)"); err != nil {
			t.Fatal(err)
		}
		if err := pg.Migrate(ctx, f.pool); err == nil {
			t.Fatal("expected migration failure")
		}
		var applied bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='004')").Scan(&applied); err != nil || applied {
			t.Fatalf("failed migration recorded: %v %v", applied, err)
		}
		if _, err := f.pool.Exec(ctx, "DROP TABLE travel_decisions"); err != nil {
			t.Fatal(err)
		}
		if err := pg.Migrate(ctx, f.pool); err != nil {
			t.Fatal(err)
		}
		if err := pg.Migrate(ctx, f.pool); err != nil {
			t.Fatal(err)
		}
		var users int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil || users != 2 {
			t.Fatalf("platform data changed: %d %v", users, err)
		}
	})
	t.Run("revision_idempotency_and_fencing", func(t *testing.T) {
		f, ref, service := travelFixture(t)
		cmd := travelCommand()
		first, err := service.Apply(ctx, ref, "create", cmd)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := service.Apply(ctx, ref, "create", cmd)
		if err != nil || replayed.Plan.ID != first.Plan.ID || replayed.Plan.Revision != 1 {
			t.Fatalf("duplicate create: %+v %v", replayed, err)
		}
		changed := travelCommand()
		changed.Tasks[0].Destination = "南京"
		if _, err = service.Apply(ctx, ref, "create", changed); !errors.Is(err, model.ErrIdempotencyConflict) {
			t.Fatalf("changed payload: %v", err)
		}
		if _, err = f.repo.GetTrip(ctx, model.Scope{UserID: "user-b"}, first.Plan.ID); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("cross-user read: %v", err)
		}
		other := ref
		other.Scope.UserID = "user-b"
		if _, err = service.Apply(ctx, other, "cross-user", travelCommand()); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("cross-user write: %v", err)
		}
		activity := []model.TripActivity{{Purpose: "开会", Date: "2026-10-25", Source: "user", Strength: model.Hard}}
		update := model.TripCommand{Operation: "amend", PlanID: first.Plan.ID, ExpectedRevision: 1, Changes: []model.TripTaskChange{{TaskID: "t1", Activities: &activity}}}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, key := range []string{"change-a", "change-b"} {
			wg.Add(1)
			go func(key string) { defer wg.Done(); _, err := service.Apply(ctx, ref, key, update); results <- err }(key)
		}
		wg.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, model.ErrConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("revision race: %d success, %d conflicts", successes, conflicts)
		}
		stored, err := f.repo.GetTrip(ctx, ref.Scope, first.Plan.ID)
		if err != nil || stored.Revision != 2 {
			t.Fatalf("revision: %+v %v", stored, err)
		}
		if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET attempt=attempt+1 WHERE id=$1", ref.RunID); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(ctx, ref, "late", travelCommand()); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("old attempt: %v", err)
		}
		ref.Attempt++
		if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET status='pausing' WHERE id=$1", ref.RunID); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(ctx, ref, "after-pause", travelCommand()); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("write after pause: %v", err)
		}
		if _, err = f.pool.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE id=$1", ref.Scope.SessionID); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Read(ctx, ref.Scope, first.Plan.ID); err == nil {
			t.Fatal("revoked session read travel business data")
		}
	})
	t.Run("receipt_atomicity_and_decisions", func(t *testing.T) {
		f, ref, service := travelFixture(t)
		decision := model.TravelDecision{RunID: ref.RunID, DecisionKey: "model-turn-1", Stage: "planning", RequestHash: "input-hash", Response: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call-1", Name: "write_trip", Arguments: `{"operation":"propose"}`}}}}
		saved, err := f.repo.AcceptTravelDecision(ctx, ref, decision)
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.repo.AcceptTravelDecision(ctx, ref, decision)
		if err != nil || saved.OperationIDs["call-1"] == "" || saved.OperationIDs["call-1"] != again.OperationIDs["call-1"] {
			t.Fatalf("unstable operations: %+v %+v %v", saved, again, err)
		}
		decision.RequestHash = "different"
		if _, err = f.repo.AcceptTravelDecision(ctx, ref, decision); !errors.Is(err, model.ErrIdempotencyConflict) {
			t.Fatalf("decision conflict: %v", err)
		}
		if _, err = f.pool.Exec(ctx, "UPDATE agent_runs SET attempt=attempt+1 WHERE id=$1", ref.RunID); err != nil {
			t.Fatal(err)
		}
		ref.Attempt++
		restored, ok, err := f.repo.FindTravelDecision(ctx, ref, saved.DecisionKey)
		if err != nil || !ok || restored.OperationIDs["call-1"] != saved.OperationIDs["call-1"] {
			t.Fatalf("operation identity changed across attempts: %+v %v", restored, err)
		}
		if _, err = f.pool.Exec(ctx, "UPDATE travel_actions SET status='unknown' WHERE operation_id=$1", saved.OperationIDs["call-1"]); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(ctx, ref, saved.OperationIDs["call-1"], travelCommand()); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("unknown write replayed: %v", err)
		}
		if _, err = f.pool.Exec(ctx, `CREATE FUNCTION reject_travel_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
CREATE TRIGGER reject_travel_receipt BEFORE INSERT ON travel_operation_receipts FOR EACH ROW EXECUTE FUNCTION reject_travel_receipt();`); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(ctx, ref, "failed-create", travelCommand()); err == nil {
			t.Fatal("expected receipt failure")
		}
		var plans, revisions, receipts int
		if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM travel_plans),(SELECT count(*) FROM travel_plan_revisions),(SELECT count(*) FROM travel_operation_receipts)").Scan(&plans, &revisions, &receipts); err != nil {
			t.Fatal(err)
		}
		if plans != 0 || revisions != 0 || receipts != 0 {
			t.Fatalf("partial write: %d %d %d", plans, revisions, receipts)
		}
	})
}
