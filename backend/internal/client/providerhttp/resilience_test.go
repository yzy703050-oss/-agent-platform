package providerhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type attemptTransport func(*http.Request) (*http.Response, error)

func (f attemptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type attemptDoer func(*http.Request) (*http.Response, error)

func (f attemptDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestRuntimeStatusRetryDelayAndTransportUnwrap(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		doer := SingleAttempt(attemptDoer(func(*http.Request) (*http.Response, error) {
			r := testResponse(429)
			r.Header.Set("Retry-After", "2")
			return r, nil
		}))
		req, _ := http.NewRequest("GET", "https://example.test", nil)
		var out any
		err := Decode(context.Background(), doer, req, &out)
		var after interface{ RetryDelay() time.Duration }
		if !errors.As(err, &after) || after.RetryDelay() != 2*time.Second {
			t.Fatalf("retry delay discarded: %v", err)
		}
	})
	t.Run("nested_transport", func(t *testing.T) {
		var count atomic.Int32
		original := &http.Client{Transport: NewResilientTransport(attemptTransport(func(*http.Request) (*http.Response, error) { count.Add(1); return testResponse(503), nil }), ResilienceOptions{Sleep: func(context.Context, time.Duration) error { return nil }})}
		req, _ := http.NewRequest("GET", "https://example.test", nil)
		response, err := SingleHTTPClient(original).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if count.Load() != 1 {
			t.Fatalf("runtime retained nested retry transport: %d", count.Load())
		}
	})
}
func testResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}
}

func TestResilienceFourPhysicalAttempts(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	var windows []time.Duration
	client := NewResilient(server.Client(), ResilienceOptions{Sleep: func(context.Context, time.Duration) error { return nil }, Jitter: func(window time.Duration) time.Duration { windows = append(windows, window); return 0 }})
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls.Load() != 4 || len(windows) != 3 || windows[0] != time.Second || windows[1] != 2*time.Second || windows[2] != 4*time.Second {
		t.Fatalf("physical attempts=%d backoff=%v", calls.Load(), windows)
	}
}

func TestSingleAttemptBypassesDefaultRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := Default(SingleAttempt(server.Client()))
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("runtime physical attempt was retried %d times", calls.Load())
	}
}

func TestTravelProviderResilience(t *testing.T) {
	t.Run("model transport owns retries and preserves POST body", func(t *testing.T) {
		calls := 0
		transport := NewResilientTransport(attemptTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			body, err := io.ReadAll(req.Body)
			if err != nil || string(body) != `{"model":"fixture"}` {
				t.Fatal("POST replay changed", err)
			}
			status := 500
			if calls == 3 {
				status = 200
			}
			return testResponse(status), nil
		}), ResilienceOptions{Sleep: func(context.Context, time.Duration) error { return nil }, Jitter: func(time.Duration) time.Duration { return 0 }})
		request, _ := http.NewRequest("POST", "https://example.test/v1/chat/completions", bytes.NewBufferString(`{"model":"fixture"}`))
		response, err := (&http.Client{Transport: transport}).Do(request)
		if err != nil || calls != 3 || response.StatusCode != 200 {
			t.Fatal(calls, err)
		}
		response.Body.Close()
	})
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
				expected = 4
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
