package travel

import (
	"context"
	"errors"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type KernelConfig struct {
	NativeFinalize      func(context.Context, string) (string, error)
	NativeModelObserved func(context.Context, *schema.Message) error
	NativeBeforeModel   func(context.Context, []*schema.Message) ([]*schema.Message, string, error)
	NativeReserveBatch  func(context.Context, []traveltools.NativeCall) error
	NativeRejected      func(context.Context, traveltools.NativeCall, *pm.TravelRuleError) (string, error)
	Registry            *traveltools.Registry
	NativeProfile       func(context.Context) (traveltools.ProfileRequest, error)
	NativeAfterBatch    func(context.Context, traveltools.PreparedBatch, []pm.TravelToolResult) (string, error)
	Model               model.ToolCallingChatModel
	MaxIterations       int
	Handlers            []adk.ChatModelAgentMiddleware
	OnState             func(context.Context, State) error
}

func NewKernel(ctx context.Context, cfg KernelConfig) (adk.ResumableAgent, error) {
	if cfg.Model == nil || cfg.Registry == nil || cfg.MaxIterations <= 0 {
		return nil, errors.New("native travel model, Registry and iteration limit are required")
	}
	return newNativeKernel(ctx, cfg)
}
