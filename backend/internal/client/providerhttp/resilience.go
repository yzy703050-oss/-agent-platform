package providerhttp

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"agent-platform/backend/internal/model"
)

var (
	ErrCircuitOpen   = errors.New("provider circuit open")
	ErrRetryDeadline = errors.New("provider retry exceeds remaining deadline")
)

type ResilienceOptions struct {
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
	Jitter func(time.Duration) time.Duration
}
type circuitLease struct {
	state      string
	generation uint64
}
type circuitEntry struct {
	state      string
	generation uint64
	failures   []time.Time
	opened     time.Time
	busy       bool
	successes  int
}
type circuitRegistry struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*circuitEntry
}

func newCircuitRegistry(now func() time.Time) *circuitRegistry {
	return &circuitRegistry{now: now, entries: map[string]*circuitEntry{}}
}
func (b *circuitRegistry) entry(key string) *circuitEntry {
	e := b.entries[key]
	if e == nil {
		e = &circuitEntry{state: "closed"}
		b.entries[key] = e
	}
	now := b.now()
	first := 0
	for first < len(e.failures) && now.Sub(e.failures[first]) >= time.Minute {
		first++
	}
	e.failures = e.failures[first:]
	if e.state == "open" && now.Sub(e.opened) >= time.Minute {
		e.state = "half_open"
		e.busy = false
		e.successes = 0
	}
	return e
}
func (b *circuitRegistry) acquire(key string) (circuitLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(key)
	if e.state == "open" || e.state == "half_open" && e.busy {
		return circuitLease{}, ErrCircuitOpen
	}
	if e.state == "half_open" {
		e.busy = true
	}
	return circuitLease{e.state, e.generation}, nil
}
func (b *circuitRegistry) success(key string, l circuitLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(key)
	if l.state == "half_open" && l.generation == e.generation && e.state == "half_open" {
		e.busy = false
		e.successes++
		if e.successes >= 2 {
			e.state = "closed"
			e.successes = 0
			e.failures = nil
		}
	}
}
func (b *circuitRegistry) abandon(key string, l circuitLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(key)
	if l.state == "half_open" && l.generation == e.generation {
		e.busy = false
	}
}
func (b *circuitRegistry) failure(key string, l circuitLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entry(key)
	if l.generation != e.generation {
		return
	}
	if e.state == "half_open" || e.state == "closed" {
		e.failures = append(e.failures, b.now())
		if e.state == "half_open" || len(e.failures) >= 5 {
			e.state = "open"
			e.opened = b.now()
			e.busy = false
			e.successes = 0
			e.generation++
		}
	}
}

// Resilient is the sole HTTP retry owner. Keys contain endpoint paths, never
// credentials in query strings; each endpoint admits at most two requests.
type Resilient struct {
	inner    Doer
	opts     ResilienceOptions
	breakers *circuitRegistry
	mu       sync.Mutex
	slots    map[string]chan struct{}
}

func NewResilient(inner Doer, opts ResilienceOptions) *Resilient {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Jitter == nil {
		opts.Jitter = func(max time.Duration) time.Duration { return time.Duration(rand.Int64N(int64(max) + 1)) }
	}
	if opts.Sleep == nil {
		opts.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return &Resilient{inner: inner, opts: opts, breakers: newCircuitRegistry(opts.Now), slots: map[string]chan struct{}{}}
}
func (r *Resilient) slot(key string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot := r.slots[key]
	if slot == nil {
		slot = make(chan struct{}, 2)
		r.slots[key] = slot
	}
	return slot
}
func sourceRetryStatus(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}
func retryDelay(header string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseFloat(header, 64); err == nil {
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			return 0
		}
		if seconds >= float64(math.MaxInt64)/float64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if stamp, err := http.ParseTime(header); err == nil && stamp.After(now) {
		return stamp.Sub(now)
	}
	return 0
}
func (r *Resilient) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	key := req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	slot := r.slot(key)
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		lease, err := r.breakers.acquire(key)
		if err != nil {
			<-slot
			return nil, err
		}
		current := req.Clone(ctx)
		if attempt > 0 && req.Body != nil {
			if req.GetBody == nil {
				<-slot
				r.breakers.abandon(key, lease)
				return nil, errors.New("provider request body is not replayable")
			}
			current.Body, err = req.GetBody()
			if err != nil {
				<-slot
				r.breakers.abandon(key, lease)
				return nil, err
			}
		}
		resp, err := r.inner.Do(current)
		<-slot
		if ctx.Err() != nil {
			r.breakers.abandon(key, lease)
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			return nil, ctx.Err()
		}
		var transport net.Error
		retry := errors.As(err, &transport) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, model.ErrProviderTransient) || resp != nil && sourceRetryStatus(resp.StatusCode)
		if !retry {
			if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				r.breakers.success(key, lease)
			} else {
				r.breakers.abandon(key, lease)
			}
			return resp, err
		}
		r.breakers.failure(key, lease)
		if attempt == 2 || req.Body != nil && req.GetBody == nil {
			return resp, err
		}
		delay := r.opts.Jitter(time.Second << attempt)
		if resp != nil {
			delay = max(delay, retryDelay(resp.Header.Get("Retry-After"), r.opts.Now()))
			if resp.Body != nil {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
			}
		}
		if deadline, ok := ctx.Deadline(); ok && delay >= deadline.Sub(r.opts.Now()) {
			return nil, ErrRetryDeadline
		}
		if err = r.opts.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, model.ErrProviderTransient
}
