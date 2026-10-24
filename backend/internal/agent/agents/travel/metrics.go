package travel

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agenttools"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Each row is one actual provider Generate invocation, including auxiliary
// roles and failed generations. Accepted-decision replay creates no new row.
// Usage=null means unknown; tokens are never estimated from character counts.
type ModelCallMeasurement struct {
	CallID         string             `json:"call_id"`
	Stage          string             `json:"stage"`
	StartedAt      time.Time          `json:"started_at"`
	FinishedAt     time.Time          `json:"finished_at"`
	DurationMS     float64            `json:"duration_ms"`
	Usage          *schema.TokenUsage `json:"usage"`
	RequestedTools []string           `json:"requested_tools"`
	ErrorCode      string             `json:"error_code,omitempty"`
}

type measuredModel struct{ inner em.ToolCallingChatModel }

func measureModel(inner em.ToolCallingChatModel) em.ToolCallingChatModel {
	if _, ok := inner.(*measuredModel); ok {
		return inner
	}
	return &measuredModel{inner: inner}
}
func (m *measuredModel) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &measuredModel{inner: inner}, nil
}
func (m *measuredModel) Generate(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	row := ModelCallMeasurement{CallID: uuid.NewString(), Stage: modelStage(ctx), StartedAt: time.Now().UTC(), RequestedTools: []string{}}
	out, err := m.inner.Generate(ctx, in, opts...)
	row.FinishedAt = time.Now().UTC()
	row.DurationMS = float64(row.FinishedAt.Sub(row.StartedAt).Microseconds()) / 1000
	if out != nil {
		if out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
			usage := *out.ResponseMeta.Usage
			row.Usage = &usage
		}
		for _, call := range out.ToolCalls {
			row.RequestedTools = append(row.RequestedTools, call.Function.Name)
		}
	}
	if err != nil {
		row.ErrorCode = "provider_error"
		if errors.Is(err, context.Canceled) {
			row.ErrorCode = "cancelled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			row.ErrorCode = "deadline_exceeded"
		}
	}
	payload, encodeErr := json.Marshal(row)
	if encodeErr != nil {
		return out, encodeErr
	}
	if emitErr := agenttools.Emit(ctx, contracts.Event{Type: "travel.model_call", Payload: payload}); emitErr != nil && err == nil {
		return out, emitErr
	}
	return out, err
}
func (m *measuredModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}
