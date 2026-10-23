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
	Policy      traveltools.Policy
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
	sum := sha256.Sum256([]byte(cfg.Identity + instruction + "travel-workflow-v2:tools-v2:eino-v0.9.0-beta.1"))
	return &Agent{cfg: cfg, fingerprint: hex.EncodeToString(sum[:])}, nil
}
func (a *Agent) Fingerprint() string { return a.fingerprint }
func (a *Agent) NewExecution(ctx context.Context, in contracts.RunInput) (contracts.Execution, error) {
	ref := pm.TravelExecution{Scope: in.Scope, ConversationID: in.Run.ConversationID, RunID: in.Run.ID, Attempt: in.Run.Attempt}
	exec := &execution{agent: a, input: in, done: make(chan struct{})}
	tools, err := traveltools.New(traveltools.Config{Trips: a.cfg.Trips, Preferences: a.cfg.Preferences, Policy: a.cfg.Policy, Execution: ref, Timeout: in.Limits.ToolTimeout})
	if err != nil {
		return nil, err
	}
	h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: a.cfg.Decisions, ref: ref}
	kernel, err := NewKernel(ctx, KernelConfig{Model: a.cfg.Model, Tools: tools, MaxIterations: in.Limits.Iterations, Handlers: []adk.ChatModelAgentMiddleware{h}, OnState: func(ctx context.Context, s State) error {
		exec.mu.Lock()
		exec.planID = s.PlanID
		exec.state = s
		exec.mu.Unlock()
		if s.PlanID != "" {
			inspection, err := a.cfg.Trips.Inspect(ctx, ref, s.PlanID)
			if err != nil {
				return err
			}
			for _, issue := range inspection.Validation.Issues {
				if issue.Code == "selection_expired" {
					return pm.ErrTravelEvidenceExpired
				}
			}
		}
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
	state           State
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
		ref := pm.TravelExecution{Scope: e.input.Scope, ConversationID: e.input.Run.ConversationID, RunID: e.input.Run.ID, Attempt: e.input.Run.Attempt}
		if err = e.agent.cfg.Context.Maintain(ctx, ref, messages); err != nil {
			return contracts.ExecutionResult{}, err
		}
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
	var queryEvidence *pm.TravelEvidence
	var policyAssessment *pm.PolicyAssessment
	var savedPreference *pm.TravelPreference
	observe := func(content string) {
		var out traveltools.TripObservation
		if json.Unmarshal([]byte(content), &out) == nil && out.Trip.Plan.ID != "" {
			finalPlanID = out.Trip.Plan.ID
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &fields) != nil {
			return
		}
		if fields["query"] != nil {
			var q pm.TravelEvidence
			if json.Unmarshal([]byte(content), &q) == nil {
				queryEvidence = &q
			}
		}
		if fields["compliance"] != nil {
			var p pm.PolicyAssessment
			if json.Unmarshal([]byte(content), &p) == nil {
				policyAssessment = &p
			}
		}
		if fields["key"] != nil {
			var p pm.TravelPreference
			if json.Unmarshal([]byte(content), &p) == nil {
				savedPreference = &p
			}
		}
	}
	err = e.runner.Run(ctx, input, func(v *schema.Message) error {
		if v.Role == schema.Tool {
			observe(v.Content)
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
	if errors.Is(err, pm.ErrTravelOutcomeUnknown) {
		code = "travel_result_unknown"
	}
	if errors.Is(err, pm.ErrTravelEvidenceExpired) {
		code = "travel_evidence_expired"
	}
	text := e.runner.Result().FinalText
	if err == nil && !e.runner.Result().Resumable && finalPlanID == "" && queryEvidence == nil && policyAssessment == nil && savedPreference == nil {
		e.mu.Lock()
		s := e.state
		e.mu.Unlock()
		if s.LastToolCallID != "" {
			ref := pm.TravelExecution{Scope: e.input.Scope, ConversationID: e.input.Run.ConversationID, RunID: e.input.Run.ID, Attempt: e.input.Run.Attempt}
			d, ok, readErr := e.agent.cfg.Decisions.FindTravelDecision(ctx, ref, decisionKey(s.LastDecision))
			if readErr != nil {
				err = readErr
			} else if !ok {
				err = pm.ErrConflict
			} else {
				out, found, readErr := e.agent.cfg.Decisions.FindTravelToolResult(ctx, ref, d.OperationIDs[s.LastToolCallID])
				if readErr != nil {
					err = readErr
				} else if !found {
					err = pm.ErrConflict
				} else {
					observe(out)
				}
			}
		}
	}
	if finalPlanID == "" {
		e.mu.Lock()
		finalPlanID = e.planID
		e.mu.Unlock()
	}
	if err == nil && !e.runner.Result().Resumable && finalPlanID != "" {
		var inspected pm.TripResult
		inspected, err = e.agent.cfg.Trips.Inspect(ctx, pm.TravelExecution{Scope: e.input.Scope, ConversationID: e.input.Run.ConversationID, RunID: e.input.Run.ID, Attempt: e.input.Run.Attempt}, finalPlanID)
		if err == nil {
			text = renderTrip(inspected.Plan)
		}
	}
	if err == nil && !e.runner.Result().Resumable && finalPlanID == "" {
		if queryEvidence != nil {
			text = renderEvidence(*queryEvidence)
		}
		if policyAssessment != nil {
			text = policyAssessment.Reason
			for _, f := range policyAssessment.Evidence.Fragments {
				text += "\n政策来源：" + f.Source + "（片段 " + f.ID + "）"
			}
		}
		if savedPreference != nil {
			text = "已保存偏好：" + savedPreference.Key
			if savedPreference.Retracted {
				text = "已撤回偏好：" + savedPreference.Key
			}
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
		if (t.Train != nil && t.Train.Candidate.IsDemo) || (t.Hotel != nil && t.Hotel.Candidate.IsDemo) {
			b.WriteString("\n演示数据：该段候选及金额仅供演示，不能用于真实出行。")
		}
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

func renderEvidence(e pm.TravelEvidence) string {
	var b strings.Builder
	if e.IsDemo {
		b.WriteString("演示数据：以下为模拟结果，不代表真实报价、余票或天气。\n")
	}
	if e.Message != "" {
		b.WriteString(e.Message + "\n")
	}
	if e.Source.Provider != "" {
		fmt.Fprintf(&b, "查询来源：%s，获取于 %s。\n", e.Source.Provider, e.Source.FetchedAt.Format("2006-01-02 15:04:05 MST"))
	}
	for i, c := range e.Candidates {
		if i >= 10 {
			break
		}
		fmt.Fprintf(&b, "\n%s", c.Name)
		if c.PriceFen != nil {
			fmt.Fprintf(&b, "，价格 %d.%02d 元", *c.PriceFen/100, *c.PriceFen%100)
		} else {
			b.WriteString("，价格与库存未核验")
		}
		if c.Address != "" {
			b.WriteString("，" + c.Address)
		}
	}
	for _, w := range e.Weather {
		fmt.Fprintf(&b, "\n%s %s", w.Date, w.Description)
		if w.TemperatureC != nil {
			fmt.Fprintf(&b, "，%.1f°C", *w.TemperatureC)
		}
	}
	if len(e.Candidates) == 0 && len(e.Weather) == 0 && e.Message == "" {
		b.WriteString("未获得可用结果。")
	}
	if e.Query.Domain != "weather" {
		b.WriteString("\n查询结果不代表已预订或付款。")
	}
	return b.String()
}
