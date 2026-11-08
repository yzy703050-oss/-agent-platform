package runtimeintegration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"agent-platform/backend/internal/permit"
	"github.com/redis/go-redis/v9"
)

func redisFixture(t *testing.T, f *runtimeFixture) (*redis.Client, permit.RedisConfig) {
	t.Helper()
	addr := os.Getenv("RUNTIME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Fatal("explicit isolated RUNTIME_TEST_REDIS_ADDR required; no skip")
	}
	cfg := permit.RedisConfig{Addr: addr, Password: os.Getenv("RUNTIME_TEST_REDIS_PASSWORD"), Prefix: f.Principals.Prefix + ":permit:"}
	r := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, MaxRetries: -1})
	if err := r.Ping(context.Background()).Err(); err != nil {
		t.Fatal("isolated Redis unavailable")
	}
	t.Cleanup(func() { deletePermitKeys(t, r, cfg.Prefix); r.Close() })
	return r, cfg
}

func deletePermitKeys(t *testing.T, r *redis.Client, prefix string) {
	t.Helper()
	ctx := context.Background()
	var cursor uint64
	for {
		keys, next, err := r.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err = r.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

// Forgetting every Redis permit represents the safety-relevant write-loss
// consequence of a failover; actual Sentinel promotion belongs to A15.
func TestRedisFailoverCannotMultiplyPermits(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	if _, err := f.Admin.Exec(ctx, `UPDATE control.run_guards SET budget_limits='{"model_attempts":20,"tool_attempts":20,"token_upper_bound":2000,"cost_upper_micros":10000}'`); err != nil {
		t.Fatal(err)
	}
	r, cfg := redisFixture(t, f)
	p, err := permit.NewRedis(f.Platform, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	child, err := permit.NewRedis(f.Travel, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	scope := issueTestScope(t, f, "parent", "platform")
	until := time.Now().UTC().Add(30 * time.Second)
	for _, id := range []string{"first", "second"} {
		if err = p.Acquire(ctx, "account", id, until); err != nil {
			t.Fatal(err)
		}
		if _, err = reserveTestAttempt(ctx, f.Platform, scope, id, []string{"account"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = settleTestAttempt(ctx, f.Platform, "first", "unknown", nil); err != nil {
		t.Fatal(err)
	}
	if err = p.Release(ctx, "account", "first"); !errors.Is(err, permit.ErrHeld) {
		t.Fatalf("in-flight permit released: %v", err)
	}
	if err = p.Acquire(ctx, "account", "first", until); err != nil {
		t.Fatalf("same attempt not idempotent: %v", err)
	}
	deletePermitKeys(t, r, cfg.Prefix)
	bounded, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if err = child.Acquire(bounded, "account", "third", until); err == nil {
		t.Fatal("lost Redis data multiplied permits")
	}
	var generation, count int
	if err = f.Admin.QueryRow(ctx, "SELECT redis_generation FROM control.resource_limits WHERE resource_key='account'").Scan(&generation); err != nil || generation < 3 {
		t.Fatalf("no PG-fenced rebuild: %d %v", generation, err)
	}
	if err = f.Admin.QueryRow(ctx, "SELECT count(*) FROM control.outbound_calls WHERE status IN('reserved','unknown')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("active calls=%d %v", count, err)
	}
	if _, err = settleTestAttempt(ctx, f.Platform, "first", "not_sent", nil); err != nil {
		t.Fatal(err)
	}
	if err = p.Release(ctx, "account", "first"); err != nil {
		t.Fatal(err)
	}
	if err = child.Acquire(ctx, "account", "third", until); err != nil {
		t.Fatalf("settled permit not released: %v", err)
	}
	if err = child.Release(ctx, "account", "third"); err != nil {
		t.Fatal(err)
	}
	if err = child.Release(ctx, "account", "third"); err != nil {
		t.Fatal(err)
	}
}

func TestRedisUnavailableBlocksAdmissionAllowsSettlement(t *testing.T) {
	f := newRuntimeFixture(t)
	f.apply(t)
	seedAuthorization(t, f)
	ctx := context.Background()
	scope := issueTestScope(t, f, "scope", "platform")
	if _, err := reserveTestAttempt(ctx, f.Platform, scope, "first", []string{"account"}); err != nil {
		t.Fatal(err)
	}
	p, err := permit.NewRedis(f.Platform, permit.RedisConfig{Addr: "127.0.0.1:1", Prefix: f.Principals.Prefix + ":unavailable:"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := time.Now()
	if err = p.Acquire(ctx, "account", "second", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("unavailable Redis admitted call")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Redis command exceeded admission deadline")
	}
	if _, err = settleTestAttempt(ctx, f.Platform, "first", "succeeded", nil); err != nil {
		t.Fatalf("Redis blocked original settlement: %v", err)
	}
}
