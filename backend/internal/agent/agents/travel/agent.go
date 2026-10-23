package travel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type KernelConfig struct {
	Model         model.ToolCallingChatModel
	Tools         []tool.BaseTool
	MaxIterations int
	Handlers      []adk.ChatModelAgentMiddleware
	OnState       func(context.Context, State) error
}

func NewKernel(ctx context.Context, cfg KernelConfig) (adk.ResumableAgent, error) {
	if cfg.Model == nil || cfg.MaxIterations <= 0 {
		return nil, errors.New("invalid travel kernel configuration")
	}
	h := &phaseHandler{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, limit: cfg.MaxIterations, onState: cfg.OnState}
	for _, t := range cfg.Tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		if info == nil || info.Name == "" {
			return nil, errors.New("invalid travel tool")
		}
		h.tools = append(h.tools, info)
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "travel", Description: "差旅行程规划", Model: cfg.Model, MaxIterations: cfg.MaxIterations,
		Instruction: instruction,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools}},
		Handlers:    append([]adk.ChatModelAgentMiddleware{h}, cfg.Handlers...),
		GenModelInput: func(ctx context.Context, instruction string, input *adk.AgentInput) ([]*schema.Message, error) {
			s, err := readState(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*schema.Message, 0, len(input.Messages)+1)
			if err = saveState(ctx, s); err != nil {
				return nil, err
			}
			out = append(out, schema.SystemMessage(instruction+" 北京日期："+s.Today+"。当前阶段："+string(s.Phase)))
			return append(out, input.Messages...), nil
		},
	})
}

type phaseHandler struct {
	*adk.BaseChatModelAgentMiddleware
	mu      sync.Mutex
	limit   int
	tools   []*schema.ToolInfo
	onState func(context.Context, State) error
}

func (h *phaseHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := readState(ctx)
	if err != nil {
		return ctx, state, err
	}
	if s.ModelCalls >= h.limit {
		return ctx, state, adk.ErrExceedMaxIterations
	}
	if len(state.Messages) > 0 && state.Messages[0].Role == schema.System && strings.HasPrefix(state.Messages[0].Content, instruction) {
		state.Messages[0] = schema.SystemMessage(instruction + " 北京日期：" + s.Today + "。当前阶段：" + string(s.Phase))
	}
	if h.onState != nil {
		if err = h.onState(ctx, s); err != nil {
			return ctx, state, err
		}
	}
	state.ToolInfos = nil
	for _, info := range h.tools {
		if allowed(s.Phase, info.Name) {
			state.ToolInfos = append(state.ToolInfos, info)
		}
	}
	return ctx, state, saveState(ctx, s)
}
func (h *phaseHandler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := readState(ctx)
	if err != nil {
		return ctx, state, err
	}
	s.ModelCalls++
	if len(state.Messages) > 0 && len(state.Messages[len(state.Messages)-1].ToolCalls) == 0 {
		s.Phase = PhaseReply
	}
	return ctx, state, saveState(ctx, s)
}
func (h *phaseHandler) WrapInvokableToolCall(_ context.Context, next adk.InvokableToolCallEndpoint, t *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		h.mu.Lock()
		s, err := readState(ctx)
		if err == nil && !allowed(s.Phase, t.Name) {
			err = errors.New("tool is not allowed in current travel phase")
		}
		h.mu.Unlock()
		if err != nil {
			return "", err
		}
		var command struct {
			Operation string `json:"operation"`
		}
		if t.Name == "write_trip" && (json.Unmarshal([]byte(args), &command) != nil || (command.Operation != "propose" && command.Operation != "select" && command.Operation != "amend")) {
			return "", errors.New("invalid trip operation")
		}
		if t.Name == "write_trip" {
			h.mu.Lock()
			s, err = readState(ctx)
			if err == nil {
				switch command.Operation {
				case "propose":
					if s.PlanAttempts >= 2 {
						err = adk.ErrExceedMaxIterations
					} else {
						s.PlanAttempts++
					}
				case "amend":
					if s.RepairAttempts >= 3 {
						err = adk.ErrExceedMaxIterations
					} else {
						s.RepairAttempts++
					}
				}
			}
			if err == nil {
				err = saveState(ctx, s)
			}
			h.mu.Unlock()
			if err != nil {
				return "", err
			}
		}
		result, err := next(ctx, args, opts...)
		if err != nil {
			return result, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		s, err = readState(ctx)
		if err != nil {
			return "", err
		}
		s.LastToolCallID = t.CallID
		s.LastDecision = s.ModelCalls - 1
		switch t.Name {
		case "write_trip":
			var observation struct {
				Trip struct {
					Plan struct {
						ID string `json:"id"`
					} `json:"plan"`
				} `json:"trip"`
			}
			if json.Unmarshal([]byte(result), &observation) == nil && observation.Trip.Plan.ID != "" {
				s.PlanID = observation.Trip.Plan.ID
			}
			if command.Operation == "propose" {
				s.Phase = PhaseQuerying
			} else {
				s.Phase = PhaseValidating
			}
		case "query_travel":
			s.Phase = PhaseValidating
		}
		return result, saveState(ctx, s)
	}, nil
}
func allowed(phase Phase, name string) bool {
	switch name {
	case "read_travel", "query_policy", "write_travel_preference":
		return phase != PhaseReply
	case "write_trip", "query_travel":
		return phase == PhasePlanning || phase == PhaseQuerying || phase == PhaseValidating
	default:
		return false
	}
}
