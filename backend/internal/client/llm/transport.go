package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"agent-platform/backend/internal/client/providerhttp"
	"github.com/cloudwego/eino/schema"
)

// The bound covers the entire HTTP envelope, including ignored JSON fields.
const responseLimit = 1000000

type runtimeTransport struct{ inner http.RoundTripper }

type responseState struct {
	usage atomic.Pointer[schema.TokenUsage]
}
type responseKey struct{}

func (s *responseState) observe(raw []byte) {
	if s == nil {
		return
	}
	var envelope struct {
		Usage *struct {
			Prompt     *int `json:"prompt_tokens"`
			Completion *int `json:"completion_tokens"`
			Total      *int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Usage == nil {
		return
	}
	u := envelope.Usage
	if u.Prompt != nil && u.Completion != nil && u.Total != nil {
		s.usage.Store(&schema.TokenUsage{PromptTokens: *u.Prompt, CompletionTokens: *u.Completion, TotalTokens: *u.Total})
	}
}

func (t runtimeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, providerhttp.ErrResponse
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, providerhttp.ResponseStatus(response)
	}
	state, _ := req.Context().Value(responseKey{}).(*responseState)
	if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		response.Body = &streamBody{ReadCloser: response.Body, state: state}
		return response, nil
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, providerhttp.ErrRequest
	}
	if len(raw) > responseLimit {
		return nil, providerhttp.ErrResponse
	}
	state.observe(raw)
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}

// A TCP EOF is not the OpenAI streaming completion marker. Preserve partial
// deltas, but make a truncated response fail instead of becoming a saved answer.
type streamBody struct {
	io.ReadCloser
	size    int
	pending []byte
	done    bool
	state   *responseState
}

func (b *streamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.size += n
	if b.size > responseLimit {
		return 0, providerhttp.ErrResponse
	}
	b.pending = append(b.pending, p[:n]...)
	for {
		i := bytes.IndexByte(b.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(b.pending[:i]))
		if line == "data: [DONE]" {
			b.done = true
		} else if strings.HasPrefix(line, "data:") {
			b.state.observe([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
		}
		b.pending = b.pending[i+1:]
	}
	if err == io.EOF && !b.done {
		return n, providerhttp.ErrRequest
	}
	return n, err
}

func responseContext(ctx context.Context) (context.Context, *responseState) {
	s := &responseState{}
	return context.WithValue(ctx, responseKey{}, s), s
}
