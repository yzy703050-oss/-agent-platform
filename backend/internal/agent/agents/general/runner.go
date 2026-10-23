package general

import "agent-platform/backend/internal/agent/einoruntime"

// Keep the existing adapter API while agents share one TurnLoop implementation.
type RunnerConfig = einoruntime.RunnerConfig
type RunResult = einoruntime.RunResult
type Runner = einoruntime.Runner

var NewRunner = einoruntime.NewRunner
