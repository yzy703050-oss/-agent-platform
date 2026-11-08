// Package permit implements shared Redis pre-admission. PostgreSQL's original
// outbound-call ledger remains the hard concurrency and budget authority.
package permit

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

//go:embed scripts/*.lua
var scripts embed.FS

var (
	ErrLimit = errors.New("shared permit unavailable")
	ErrHeld  = errors.New("attempt is still held by the PostgreSQL ledger")
)

type Service interface {
	Acquire(context.Context, string, string, time.Time) error
	Release(context.Context, string, string) error
}

type RedisConfig struct {
	Addr, Username, Password, Prefix   string
	SentinelMaster                     string
	SentinelAddrs                      []string
	SentinelUsername, SentinelPassword string
}

type Redis struct {
	pool             *pgxpool.Pool
	client           *redis.Client
	prefix           string
	acquire, release string
}

func NewRedis(pool *pgxpool.Pool, cfg RedisConfig) (*Redis, error) {
	if pool == nil || cfg.Prefix == "" || (cfg.Addr == "") == (cfg.SentinelMaster == "") || (cfg.SentinelMaster != "" && len(cfg.SentinelAddrs) == 0) {
		return nil, fmt.Errorf("explicit database, Redis mode and namespace required")
	}
	var client *redis.Client
	if cfg.SentinelMaster != "" {
		client = redis.NewFailoverClient(&redis.FailoverOptions{MasterName: cfg.SentinelMaster, SentinelAddrs: cfg.SentinelAddrs, SentinelUsername: cfg.SentinelUsername, SentinelPassword: cfg.SentinelPassword, Username: cfg.Username, Password: cfg.Password, MaxRetries: -1, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, PoolTimeout: 2 * time.Second, ContextTimeoutEnabled: true, DisableIdentity: true})
	} else {
		client = redis.NewClient(&redis.Options{Addr: cfg.Addr, Username: cfg.Username, Password: cfg.Password, MaxRetries: -1, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, PoolTimeout: 2 * time.Second, ContextTimeoutEnabled: true, DisableIdentity: true})
	}
	a, _ := scripts.ReadFile("scripts/acquire.lua")
	r, _ := scripts.ReadFile("scripts/release.lua")
	return &Redis{pool: pool, client: client, prefix: cfg.Prefix, acquire: string(a), release: string(r)}, nil
}

func (r *Redis) Close() error { return r.client.Close() }

type hold struct {
	AttemptID string `json:"attempt_id"`
	UntilMS   int64  `json:"until_ms"`
}
type snapshot struct {
	Generation int64  `json:"generation"`
	Limit      int    `json:"limit"`
	Holds      []hold `json:"holds"`
}

func readSnapshot(ctx context.Context, tx pgx.Tx, key string, expected *int64) (snapshot, error) {
	var s snapshot
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT control.resource_snapshot($1,$2)", key, expected).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &s)
	}
	return s, err
}

func (r *Redis) key(resource string) string {
	h := sha256.Sum256([]byte(resource))
	return r.prefix + hex.EncodeToString(h[:])
}

func (r *Redis) Acquire(ctx context.Context, resource, attempt string, until time.Time) error {
	if resource == "" || attempt == "" || !until.After(time.Now()) {
		return fmt.Errorf("invalid permit identity or expiry")
	}
	deadline := time.Now().Add(2 * time.Second)
	if until.Before(deadline) {
		deadline = until
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// A reachable replica or uncertain role is not permission to send work.
	role, err := r.client.Do(ctx, "ROLE").Slice()
	if err != nil {
		return fmt.Errorf("Redis primary unavailable: %w", err)
	}
	if len(role) == 0 || role[0] != "master" {
		return fmt.Errorf("Redis primary role unconfirmed")
	}
	for {
		ok, err := r.tryAcquire(ctx, resource, attempt, until)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %w", ErrLimit, ctx.Err())
		case <-timer.C:
		}
	}
}

func (r *Redis) tryAcquire(ctx context.Context, resource, attempt string, until time.Time) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	s, err := readSnapshot(ctx, tx, resource, nil)
	if err != nil {
		return false, err
	}
	key := r.key(resource)
	args := []any{s.Generation, s.Limit, attempt, until.UnixMilli(), "acquire", "[]"}
	code, err := r.client.Eval(ctx, r.acquire, []string{key}, args...).Int()
	if err != nil {
		return false, err
	}
	if code == -1 {
		s, err = readSnapshot(ctx, tx, resource, &s.Generation)
		if err != nil {
			return false, err
		}
		b, err := json.Marshal(s.Holds)
		if err != nil {
			return false, err
		}
		args = []any{s.Generation, s.Limit, attempt, until.UnixMilli(), "rebuild", string(b)}
		code, err = r.client.Eval(ctx, r.acquire, []string{key}, args...).Int()
		if err != nil {
			return false, err
		}
	}
	if code < 0 {
		return false, fmt.Errorf("permit expiry or generation rejected")
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return code == 1, nil
}

func (r *Redis) Release(ctx context.Context, resource, attempt string) error {
	if resource == "" || attempt == "" {
		return fmt.Errorf("invalid permit identity")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	s, err := readSnapshot(ctx, tx, resource, nil)
	if err != nil {
		return err
	}
	for _, h := range s.Holds {
		if h.AttemptID == attempt {
			return ErrHeld
		}
	}
	code, err := r.client.Eval(ctx, r.release, []string{r.key(resource)}, s.Generation, attempt).Int()
	if err != nil {
		return err
	}
	if code < 0 {
		return fmt.Errorf("Redis generation changed; permit retained")
	}
	return tx.Commit(ctx)
}
