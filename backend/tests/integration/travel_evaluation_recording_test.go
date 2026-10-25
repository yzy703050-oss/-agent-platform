package integration

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type perfModelInput struct {
	Sequence   int                `json:"sequence"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	Messages   []*schema.Message  `json:"messages"`
	Tools      []*schema.ToolInfo `json:"tools"`
	Error      string             `json:"error,omitempty"`
}
type perfInputLog struct {
	mu   sync.Mutex
	rows []perfModelInput
}
type perfRecordingModel struct {
	inner em.ToolCallingChatModel
	tools []*schema.ToolInfo
	log   *perfInputLog
}

func (m *perfRecordingModel) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &perfRecordingModel{inner: inner, tools: tools, log: m.log}, nil
}
func (m *perfRecordingModel) Generate(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	m.log.mu.Lock()
	index := len(m.log.rows)
	m.log.rows = append(m.log.rows, perfModelInput{Sequence: index + 1, StartedAt: time.Now().UTC(), Messages: perfInputCopy(in), Tools: perfInputCopy(m.tools)})
	m.log.mu.Unlock()
	out, err := m.inner.Generate(ctx, in, opts...)
	m.log.mu.Lock()
	m.log.rows[index].FinishedAt = time.Now().UTC()
	if err != nil {
		m.log.rows[index].Error = perfRedact(err.Error())
	}
	m.log.mu.Unlock()
	return out, err
}
func (m *perfRecordingModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}
func perfInputCopy[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal([]byte(perfRedact(string(raw))), &out)
	return out
}
