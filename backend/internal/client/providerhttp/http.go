package providerhttp

import (
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

var ErrResponse = errors.New("invalid provider response")
var ErrRequest = fmt.Errorf("provider request failed: %w", model.ErrProviderTransient)

func Default(client Doer) Doer {
	if _, ok := client.(*singleAttempt); ok {
		return client
	}
	if _, ok := client.(*Resilient); ok {
		return client
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return NewResilient(client, ResilienceOptions{})
}

type singleAttempt struct{ inner Doer }

func (s *singleAttempt) Do(req *http.Request) (*http.Response, error) { return s.inner.Do(req) }

// SingleAttempt marks a runtime-owned physical call so provider constructors
// cannot add their legacy retry loop. Runtime callers own durable retries.
func SingleAttempt(inner Doer) Doer {
	for {
		switch c := inner.(type) {
		case *singleAttempt:
			return c
		case *Resilient:
			inner = c.inner
		case *http.Client:
			return &singleAttempt{inner: SingleHTTPClient(c)}
		case nil:
			return &singleAttempt{inner: SingleHTTPClient(nil)}
		default:
			return &singleAttempt{inner: inner}
		}
	}
}

// SingleHTTPClient disables redirect sends and TCP connection reuse, which
// prevents net/http from transparently replaying an idempotent request on a
// broken reused connection. Sessions may still be reused at the protocol layer.
func SingleHTTPClient(original *http.Client) *http.Client {
	client := http.Client{Timeout: 10 * time.Second}
	if original != nil {
		client = *original
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	// Unwrap our own legacy buffering/retry transport before cloning TCP policy.
	if retry, ok := transport.(*resilientTransport); ok {
		if buffered, ok := retry.doer.inner.(bufferedTransportDoer); ok {
			transport = buffered.inner
		}
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	if t, ok := transport.(*http.Transport); ok {
		clone := t.Clone()
		clone.DisableKeepAlives = true
		transport = clone
	}
	client.Transport = transport
	return &client
}
func Decode(ctx context.Context, client Doer, req *http.Request, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrRequest
	}
	if resp == nil || resp.Body == nil {
		return ErrResponse
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ResponseStatus(resp)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return ErrRequest
	}
	if len(data) > limit {
		return ErrResponse
	}
	if json.Unmarshal(data, out) != nil {
		return ErrResponse
	}
	return nil
}

type StatusError struct {
	Code  int
	delay time.Duration
}

func (e *StatusError) Error() string { return fmt.Sprintf("provider HTTP status %d", e.Code) }
func (e *StatusError) Unwrap() error {
	if sourceRetryStatus(e.Code) {
		return model.ErrProviderTransient
	}
	return nil
}
func (e *StatusError) RetryDelay() time.Duration { return e.delay }
func ResponseStatus(response *http.Response) *StatusError {
	return &StatusError{Code: response.StatusCode, delay: retryDelay(response.Header.Get("Retry-After"), time.Now())}
}
