package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type HTTPConfig struct {
	Endpoint, ServiceID, BearerToken string
	Collections                      []string
	RequestTimeout                   time.Duration
}

// One client/session is bound to one configured service identity. It is never
// selected by a user argument and never inherits a browser cookie or user token.
type HTTPClient struct {
	cfg     HTTPConfig
	http    *http.Client
	mu      sync.Mutex
	session *sdk.ClientSession
	closed  bool
}

func NewHTTPClient(cfg HTTPConfig) (*HTTPClient, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) || cfg.ServiceID == "" || strings.TrimSpace(cfg.BearerToken) == "" || strings.ContainsAny(cfg.BearerToken, "\r\n") || cfg.RequestTimeout <= 0 || len(cfg.Collections) == 0 {
		return nil, model.ErrInvalidInput
	}
	for _, collection := range cfg.Collections {
		if _, err = NewPolicyClient(&unconnected{}, collection); err != nil {
			return nil, err
		}
	}
	cfg.Collections = append([]string(nil), cfg.Collections...)
	client := providerhttp.SingleHTTPClient(&http.Client{Timeout: cfg.RequestTimeout})
	// MCP only sends POST/DELETE here. Retain a service-local connection pool;
	// the transport below removes body replay and idempotency-key opt-ins.
	if transport, ok := client.Transport.(*http.Transport); ok {
		pool := transport.Clone()
		pool.DisableKeepAlives = false
		client.Transport = pool
	}
	client.Transport = &httpTransport{inner: client.Transport, bearer: cfg.BearerToken}
	return &HTTPClient{cfg: cfg, http: client}, nil
}

func (c *HTTPClient) connect(ctx context.Context) (*sdk.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("HTTP MCP client closed")
	}
	if c.session != nil {
		return c.session, nil
	}
	client := sdk.NewClient(&sdk.Implementation{Name: c.cfg.ServiceID, Version: "runtime-v1"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: c.cfg.Endpoint, HTTPClient: c.http, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: 1000000}, &sdk.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		return nil, errors.New("HTTP MCP initialization unavailable")
	}
	c.session = session
	return session, nil
}

func (c *HTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

type httpTransport struct {
	inner  http.RoundTripper
	bearer string
}

func (t *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		return nil, errors.New("standalone MCP SSE disabled")
	}
	req = req.Clone(req.Context())
	req.Header = req.Header.Clone()
	req.GetBody = nil
	req.Header.Del("Idempotency-Key")
	req.Header.Del("X-Idempotency-Key")
	req.Header.Set("Authorization", "Bearer "+t.bearer)
	response, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, providerhttp.ErrRequest
	}
	if response == nil || response.Body == nil {
		return nil, ErrProtocol
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerhttp.ResponseStatus(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1000001))
	if err != nil {
		return nil, providerhttp.ErrRequest
	}
	if len(raw) > 1000000 {
		return nil, ErrProtocol
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}
