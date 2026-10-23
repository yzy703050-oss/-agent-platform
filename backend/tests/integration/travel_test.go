package integration

import (
	"context"
	"errors"
	"sync"
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
