package providerhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type attemptDoer func(*http.Request) (*http.Response, error)

func (f attemptDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func testResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}
}

func TestTravelProviderResilience(t *testing.T) {
	t.Run("source retry statuses and extra retry limit", func(t *testing.T) {
		for _, status := range []int{400, 401, 403, 404, 429, 500, 501, 502, 503, 504, 505} {
			calls := 0
			r := NewResilient(attemptDoer(func(*http.Request) (*http.Response, error) { calls++; return testResponse(status), nil }), ResilienceOptions{Sleep: func(context.Context, time.Duration) error { return nil }, Jitter: func(time.Duration) time.Duration { return 0 }})
			req, _ := http.NewRequestWithContext(context.Background(), "GET", "https://example.test/train?key=secret", nil)
			resp, err := r.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			expected := 1
			if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
				expected = 3
			}
			if calls != expected {
				t.Errorf("status %d calls=%d want %d", status, calls, expected)
			}
		}
	})
	t.Run("retry after cannot exceed remaining budget", func(t *testing.T) {
		calls, sleeps := 0, 0
		r := NewResilient(attemptDoer(func(*http.Request) (*http.Response, error) {
			calls++
			resp := testResponse(429)
			resp.Header.Set("Retry-After", "60")
			return resp, nil
		}), ResilienceOptions{Sleep: func(context.Context, time.Duration) error { sleeps++; return nil }})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.test/train", nil)
		_, err := r.Do(req)
		if !errors.Is(err, ErrRetryDeadline) || calls != 1 || sleeps != 0 {
			t.Fatalf("calls=%d sleeps=%d err=%v", calls, sleeps, err)
		}
	})
	t.Run("sliding window half open cancellation and two successes", func(t *testing.T) {
		now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		b := newCircuitRegistry(func() time.Time { return now })
		key := "https://example.test/train"
		for i := 0; i < 5; i++ {
			lease, err := b.acquire(key)
			if err != nil {
				t.Fatal(err)
			}
			b.failure(key, lease)
		}
		if _, err := b.acquire(key); !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("five failures must open circuit", err)
		}
		now = now.Add(60 * time.Second)
		probe, err := b.acquire(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.acquire(key); !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("parallel half open probe admitted")
		}
		b.abandon(key, probe)
		probe, err = b.acquire(key)
		if err != nil {
			t.Fatal("cancellation leaked half open slot", err)
		}
		b.success(key, probe)
		probe, err = b.acquire(key)
		if err != nil {
			t.Fatal(err)
		}
		if probe.state != "half_open" {
			t.Fatal("one success closed circuit")
		}
		b.success(key, probe)
		probe, err = b.acquire(key)
		if err != nil || probe.state != "closed" {
			t.Fatal("two successes did not recover", err)
		}
		// Failures outside the window do not combine with new failures.
		b.failure(key, probe)
		now = now.Add(60 * time.Second)
		for i := 0; i < 4; i++ {
			probe, err = b.acquire(key)
			if err != nil {
				t.Fatal(err)
			}
			b.failure(key, probe)
		}
		if _, err = b.acquire(key); err != nil {
			t.Fatal("expired failures counted", err)
		}
	})
	t.Run("cancellation is never retried", func(t *testing.T) {
		calls := 0
		ctx, cancel := context.WithCancel(context.Background())
		r := NewResilient(attemptDoer(func(*http.Request) (*http.Response, error) { calls++; cancel(); return nil, context.Canceled }), ResilienceOptions{})
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.test/train", nil)
		_, err := r.Do(req)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
}
