package providerhttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

type resilientTransport struct{ doer *Resilient }

func (t *resilientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.doer.Do(req)
}

type bufferedTransportDoer struct {
	inner   http.RoundTripper
	timeout time.Duration
}

func (d bufferedTransportDoer) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cancel := func() {}
	if d.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, d.timeout)
	}
	defer cancel()
	response, err := d.inner.RoundTrip(req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrResponse
	}
	defer response.Body.Close()
	// Travel uses complete nonstreaming snapshots. Read the body inside the retry
	// owner so truncated network responses cannot escape as successful attempts.
	const maxBody = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, ErrResponse
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func NewResilientTransport(inner http.RoundTripper, opts ResilienceOptions) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &resilientTransport{doer: NewResilient(bufferedTransportDoer{inner: inner, timeout: opts.RequestTimeout}, opts)}
}
