package travel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/einoruntime"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/agenttools"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Config struct {
	Model       model.ToolCallingChatModel
	Trips       traveltools.Trips
	Preferences traveltools.Preferences
	Decisions   journalStore
	Checkpoints ports.CheckpointStore
	Context     *memory.TravelContext
	Identity    string
}
type Agent struct {
	cfg         Config
	fingerprint string
}

func NewAgent(cfg Config) (*Agent, error) {
	if cfg.Model == nil || cfg.Trips == nil || cfg.Preferences == nil || cfg.Decisions == nil || cfg.Checkpoints == nil || cfg.Identity == "" {
		return nil, errors.New("travel dependencies unavailable")
	}
	sum := sha256.Sum256([]byte(cfg.Identity + instruction + "travel-workflow-v1:tools-v1:eino-v0.9.0-beta.1"))
	return &Agent{cfg: cfg, fingerprint: hex.EncodeToString(sum[:])}, nil
}
func (a *Agent) Fingerprint() string { return a.fingerprint }
func (a *Agent) NewExecution(ctx context.Context, in contracts.RunInput) (contracts.Execution, error) {
	ref := pm.TravelExecution{Scope: in.Scope, ConversationID: in.Run.ConversationID, RunID: in.Run.ID, Attempt: in.Run.Attempt}
	exec := &execution{agent: a, input: in, done: make(chan struct{})}
	tools, err := traveltools.New(traveltools.Config{Trips: a.cfg.Trips, Preferences: a.cfg.Preferences, Execution: ref, Timeout: in.Limits.ToolTimeout})
	if err != nil {
		return nil, err
	}
	h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: a.cfg.Decisions, ref: ref}
	kernel, err := NewKernel(ctx, KernelConfig{Model: a.cfg.Model, Tools: tools, MaxIterations: in.Limits.Iterations, Handlers: []adk.ChatModelAgentMiddleware{h}, OnState: func(ctx context.Context, s State) error {
		exec.mu.Lock()
		exec.planID = s.PlanID
		exec.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"phase": s.Phase, "plan_id": s.PlanID})
		return agenttools.Emit(ctx, contracts.Event{Type: "travel.phase", Payload: b})
	}})
	if err != nil {
		return nil, err
	}
	runner, err := einoruntime.NewRunner(ctx, einoruntime.RunnerConfig{Agent: kernel, Store: &einoruntime.RepositoryStore{Store: a.cfg.Checkpoints, Input: in, Fingerprint: a.fingerprint}, RunID: in.Run.ID, Fingerprint: a.fingerprint, Resume: in.Resume, PauseTimeout: in.Limits.PauseTimeout})
	if err != nil {
		return nil, err
	}
	// No history, model, query, or mutation is executed here. Reconciliation may
	// construct this object solely to check checkpoint compatibility.
	exec.runner = runner
	return exec, nil
}

type execution struct {
	agent           *Agent
	input           contracts.RunInput
	runner          *einoruntime.Runner
	done            chan struct{}
	mu              sync.Mutex
	err             error
	cancel          context.CancelFunc
	cancelRequested bool
	planID          string
}

func (e *execution) Start(ctx context.Context, emit func(contracts.Event) error) (contracts.ExecutionResult, error) {
	ctx = agenttools.WithObserver(ctx, emit)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer close(e.done)
	e.mu.Lock()
	e.cancel = cancel
	if e.cancelRequested {
		cancel()
	}
	e.mu.Unlock()
	var err error
	defer func() { e.mu.Lock(); e.err = err; e.mu.Unlock() }()
	if ctx.Err() != nil {
		err = ctx.Err()
		return contracts.ExecutionResult{}, err
	}
	messages := e.input.Messages
	if !e.input.Resume && e.agent.cfg.Context != nil {
		messages, err = e.agent.cfg.Context.Build(ctx, e.input.Scope, e.input.Run.ConversationID, e.input.Run.ID, messages)
		if err != nil {
			return contracts.ExecutionResult{}, err
		}
	}
	input := make([]*schema.Message, 0, len(messages))
	for _, m := range messages {
		input = append(input, toEino(m))
	}
	var finalPlanID string
	err = e.runner.Run(ctx, input, func(v *schema.Message) error {
		if v.Role == schema.Tool {
			var out traveltools.TripObservation
			if json.Unmarshal([]byte(v.Content), &out) == nil && out.Trip.Receipt != nil {
				finalPlanID = out.Trip.Plan.ID
			}
		}
		if emit == nil || (v.Role != schema.Tool && len(v.ToolCalls) == 0) {
			return nil
		}
		b, _ := json.Marshal(fromEino(v))
		return emit(contracts.Event{Type: "message", Payload: b})
	})
	code := ""
	if errors.Is(err, adk.ErrExceedMaxIterations) {
		code = "iteration_limit"
	}
	if errors.Is(err, agenttools.ErrToolTimeout) {
		code = "tool_timeout"
	}
	text := e.runner.Result().FinalText
	if finalPlanID == "" {
		e.mu.Lock()
		finalPlanID = e.planID
		e.mu.Unlock()
	}
	if err == nil && !e.runner.Result().Resumable && finalPlanID != "" {
		var p pm.TripPlan
		p, err = e.agent.cfg.Trips.Read(ctx, e.input.Scope, finalPlanID)
		if err == nil {
			text = renderTrip(p)
		}
	}
	return contracts.ExecutionResult{FinalText: text, ErrorCode: code}, err
}
func (e *execution) Pause(ctx context.Context) (contracts.PauseResult, error) {
	if !e.runner.RequestPause() {
		return contracts.PauseResult{}, pm.ErrConflict
	}
	select {
	case <-ctx.Done():
		return contracts.PauseResult{}, ctx.Err()
	case <-e.done:
	}
	e.mu.Lock()
	err := e.err
	e.mu.Unlock()
	return contracts.PauseResult{Stopped: true, CheckpointSaved: e.runner.Result().Resumable}, err
}
func (e *execution) Cancel(ctx context.Context) error {
	e.mu.Lock()
	e.cancelRequested = true
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return nil
	}
}
func renderTrip(p pm.TripPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "行程已保存（修订 %d），校验状态：%s。\n", p.Revision, p.Validation.Status)
	for _, t := range p.Tasks {
		fmt.Fprintf(&b, "\n%s → %s，%s", t.Origin, t.Destination, t.DepartureDate)
		if t.Train != nil {
			fmt.Fprintf(&b, "\n车次：%s", t.Train.Candidate.Name)
			if price := t.Train.Candidate.PriceFen; price != nil {
				fmt.Fprintf(&b, "，票价 %d.%02d 元", *price/100, *price%100)
			}
		} else {
			b.WriteString("\n车次尚未选择。")
		}
		if t.RequiresHotel {
			if t.Hotel != nil {
				fmt.Fprintf(&b, "\n酒店地点：%s；房价和库存需另行确认。", t.Hotel.Candidate.Name)
			} else {
				b.WriteString("\n酒店尚未选择。")
			}
		}
	}
	for _, issue := range p.Validation.Issues {
		fmt.Fprintf(&b, "\n待确认：%s", issue.Message)
	}
	b.WriteString("\n以上为规划与查询结果，尚未预订或付款。")
	return b.String()
}
