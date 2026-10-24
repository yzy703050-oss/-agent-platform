package contracts

import (
	"agent-platform/backend/internal/model"
	"context"
	"time"
)

type Limits struct {
	Iterations                    int
	ExecutionTimeout, ToolTimeout time.Duration
	PauseTimeout                  time.Duration
}
type RunInput struct {
	Scope        model.Scope
	Run          model.Run
	Messages     []model.Message
	Resume       bool
	CheckpointID string
	Limits       Limits
}
type ExecutionResult struct{ FinalText, ErrorCode string }
type PauseResult struct {
	Stopped, CheckpointSaved bool
	ErrorCode                string
}
type Execution interface {
	Start(context.Context, func(Event) error) (ExecutionResult, error)
	Pause(context.Context) (PauseResult, error)
	Cancel(context.Context) error
}
type Agent interface {
	NewExecution(context.Context, RunInput) (Execution, error)
}

type ContextPolicy struct{ PreserveCompleteHistory bool }
type ContextPolicyProvider interface{ ContextPolicy() ContextPolicy }
