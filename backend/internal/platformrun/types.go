package platformrun

import (
	"fmt"
	"math"
	"strings"
	"time"

	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model/runtimev1"
)

type Budget = control.Reservation

type Config struct {
	RunTimeout, LeaseDuration, ToolTimeout    time.Duration
	Budget                                    Budget
	MaxModelInputTokens, MaxModelOutputTokens int64
	KernelVersion, ConfigFingerprint          string
	ScopeTargets                              []string
}

func (c Config) Normalized() (Config, error) {
	if c.RunTimeout == 0 {
		c.RunTimeout = 300 * time.Second
	}
	if c.LeaseDuration == 0 {
		c.LeaseDuration = 20 * time.Second
	}
	if c.ToolTimeout == 0 {
		c.ToolTimeout = 10 * time.Second
	}
	if c.RunTimeout < time.Microsecond || c.LeaseDuration < time.Microsecond || c.ToolTimeout < time.Microsecond || c.ToolTimeout > c.RunTimeout ||
		c.RunTimeout%time.Microsecond != 0 || c.LeaseDuration%time.Microsecond != 0 || c.ToolTimeout%time.Microsecond != 0 ||
		c.Budget.ModelAttempts < 1 || c.Budget.ModelAttempts > math.MaxInt32 || c.Budget.ToolAttempts < 1 || c.Budget.ToolAttempts > math.MaxInt32 || c.Budget.TokenUpperBound < 1 || c.Budget.CostUpperMicros < 0 ||
		c.MaxModelInputTokens < 1 || c.MaxModelOutputTokens < 1 || c.MaxModelInputTokens > c.Budget.TokenUpperBound || c.MaxModelOutputTokens > c.Budget.TokenUpperBound-c.MaxModelInputTokens ||
		strings.TrimSpace(c.KernelVersion) == "" || strings.TrimSpace(c.ConfigFingerprint) == "" {
		return Config{}, fmt.Errorf("invalid entry runtime limits or compatibility identity")
	}
	c.ScopeTargets = append([]string(nil), c.ScopeTargets...)
	for _, target := range c.ScopeTargets {
		if strings.TrimSpace(target) == "" {
			return Config{}, fmt.Errorf("empty execution target")
		}
	}
	return c, nil
}

func (c Config) Snapshot(deadline time.Time) runtimev1.LimitSnapshot {
	return runtimev1.LimitSnapshot{MaxModelAttempts: int(c.Budget.ModelAttempts), MaxToolAttempts: int(c.Budget.ToolAttempts), TokenUpperBound: c.Budget.TokenUpperBound,
		CostUpperMicros: c.Budget.CostUpperMicros, MaxModelInputTokens: c.MaxModelInputTokens, MaxModelOutputTokens: c.MaxModelOutputTokens, ToolTimeout: c.ToolTimeout, DeadlineAt: deadline}
}

type SubmitInput struct {
	ConversationID, IdempotencyKey, Content, RequestHash string
}

type FinishInput struct {
	Status, Code, FinalText string
	ExpectedRunVersion      int64
	PartialRefs             []runtimev1.SourceRef
}
