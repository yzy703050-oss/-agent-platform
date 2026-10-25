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
	"time"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/einoruntime"
	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/agenttools"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Config struct {
	Memory         traveltools.NativeMemory
	Retriever      tripsvc.PolicyRetriever
	SourcePolicy   traveltools.NativePolicy
	History        traveltools.NativeHistory
	PlanningRounds int
	ContextTokens  int
	TurnTimeout    time.Duration
	Model          model.ToolCallingChatModel
	Trips          traveltools.Trips
	Preferences    traveltools.Preferences
	Policy         traveltools.Policy
	Decisions      journalStore
	Checkpoints    ports.CheckpointStore
	Context        *memory.TravelContext
	Identity       string
}
type Agent struct {
	preference  *PreferenceAgent
	cfg         Config
	fingerprint string
}

func NewAgent(cfg Config) (*Agent, error) {
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = 20000
	}
	if cfg.Model == nil || cfg.Trips == nil || cfg.Preferences == nil || cfg.Decisions == nil || cfg.Checkpoints == nil || cfg.Identity == "" {
		return nil, errors.New("travel dependencies unavailable")
	}
	cfg.Model = measureModel(cfg.Model)
	if cfg.SourcePolicy == nil {
		cfg.SourcePolicy = NewPolicyAgent(cfg.Model, cfg.Retriever)
	}
	if cfg.History == nil {
		cfg.History, _ = cfg.Trips.(traveltools.NativeHistory)
	}
	var preference *PreferenceAgent
	if history, ok := cfg.Trips.(preferenceHistory); ok {
		if mem, ok := cfg.Preferences.(preferenceMemory); ok {
			preference = &PreferenceAgent{model: cfg.Model, history: history, memory: mem}
			if cfg.Memory == nil {
				cfg.Memory = preference
			}
		}
	}
	if cfg.Memory == nil {
		cfg.Memory, _ = cfg.Preferences.(traveltools.NativeMemory)
	}
	profiles := map[string]any{}
	for _, name := range []string{"main", "pre_policy", "review", "review_retry", "summary"} {
		profile, err := traveltools.SourceProfile(name)
		if err != nil {
			return nil, err
		}
		profiles[name] = profile
	}
	contractsJSON, err := json.Marshal(profiles)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(cfg.Identity + instruction + reviewInstruction + summaryInstruction + replyPolicy + policyPrefix + preferencePrefix + compactionPrefix + correctionInstruction + string(contractsJSON) + "travel-source-flow-v5:batch-v5:correction-v1:durable-query-v1:generation-temp0.3-max8192-deepseek-thinking-disabled:eino-v0.9.0-beta.1"))
	return &Agent{cfg: cfg, preference: preference, fingerprint: hex.EncodeToString(sum[:])}, nil
}
func (a *Agent) Fingerprint() string { return a.fingerprint }
func (a *Agent) ContextPolicy() contracts.ContextPolicy {
	return contracts.ContextPolicy{PreserveCompleteHistory: true}
}
func (a *Agent) NewExecution(ctx context.Context, in contracts.RunInput) (contracts.Execution, error) {
	ref := pm.TravelExecution{Scope: in.Scope, ConversationID: in.Run.ConversationID, RunID: in.Run.ID, Attempt: in.Run.Attempt}
	exec := &execution{agent: a, input: in, done: make(chan struct{})}
	query, initialPlans := sourceInput(in)
	flow := newSourceFlow(flowConfig{Trips: a.cfg.Trips, Execution: ref, OriginalQuery: query, InitialPlanIDs: initialPlans, PlanningRounds: a.cfg.PlanningRounds, Timeout: a.cfg.TurnTimeout, Snapshot: func(s FlowState) {
		exec.mu.Lock()
		exec.source = s
		if len(s.Plans) > 0 {
			exec.planID = s.planIDs()[0]
		}
		exec.mu.Unlock()
	}})
	flow.cfg.Model = a.cfg.Model
	flow.cfg.Decisions = a.cfg.Decisions
	flow.cfg.Preference = a.preference
	flow.cfg.History = in.Messages
	flow.cfg.ContextTokens = a.cfg.ContextTokens
	if flow.cfg.ContextTokens <= 0 {
		flow.cfg.ContextTokens = 20000
	}
	if a.cfg.Context != nil {
		flow.cfg.Summaries = a.cfg.Context.SourceSummaryStore()
	}
	if reader, ok := a.cfg.Preferences.(memory.TravelPreferenceReader); ok {
		flow.cfg.ReadPreferences = func(ctx context.Context) ([]pm.TravelPreference, error) {
			return reader.ReadEffective(ctx, ref.Scope, ref.RunID)
		}
	}
	registry, err := traveltools.NewRegistry(traveltools.NativeConfig{Config: traveltools.Config{Trips: a.cfg.Trips, Execution: ref, Timeout: in.Limits.ToolTimeout}, Context: flow.decision, Memory: a.cfg.Memory, History: a.cfg.History, SourcePolicy: a.cfg.SourcePolicy, OnNativeTrip: flow.recordTrip, OperationID: func(ctx context.Context, callID string) (string, error) {
		s, err := readState(ctx)
		if err != nil {
			return "", err
		}
		d, ok, err := a.cfg.Decisions.FindTravelDecision(ctx, ref, decisionKey(s.ModelCalls-1))
		if err != nil {
			return "", err
		}
		if !ok || d.OperationIDs[callID] == "" {
			return "", pm.ErrConflict
		}
		return d.OperationIDs[callID], nil
	}})
	if err != nil {
		return nil, err
	}
	flow.registry = registry
	h := &journal{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: a.cfg.Decisions, ref: ref, native: true, flow: flow, contextTokens: a.cfg.ContextTokens}
	kernel, err := NewKernel(ctx, KernelConfig{Model: a.cfg.Model, Registry: registry, NativeFinalize: flow.finalize, NativeBeforeModel: flow.beforeModel, NativeModelObserved: flow.modelObserved, NativeReserveBatch: flow.reserve, NativeProfile: flow.profile, NativeAfterBatch: flow.afterBatch, NativeRejected: h.recordRejected, MaxIterations: in.Limits.Iterations, Handlers: []adk.ChatModelAgentMiddleware{h}, OnState: func(ctx context.Context, s State) error {
		exec.mu.Lock()
		if s.PlanID != "" {
			exec.planID = s.PlanID
		}
		exec.state = s
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
	state           State
	source          FlowState
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
	var resultErr error
	defer func() { e.mu.Lock(); e.err = resultErr; e.mu.Unlock() }()
	if ctx.Err() != nil {
		resultErr = ctx.Err()
		return contracts.ExecutionResult{}, resultErr
	}
	messages := e.input.Messages
	input := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		input = append(input, toEino(msg))
	}
	resultErr = e.runner.Run(ctx, input, func(msg *schema.Message) error {
		if emit == nil || (msg.Role != schema.Tool && len(msg.ToolCalls) == 0) {
			return nil
		}
		data, _ := json.Marshal(fromEino(msg))
		return emit(contracts.Event{Type: "message", Payload: data})
	})
	code := ""
	switch {
	case errors.Is(resultErr, adk.ErrExceedMaxIterations):
		code = "iteration_limit"
	case errors.Is(resultErr, agenttools.ErrToolTimeout):
		code = "tool_timeout"
	case errors.Is(resultErr, pm.ErrTravelOutcomeUnknown):
		code = "travel_result_unknown"
	case errors.Is(resultErr, memory.ErrTravelContextBudget):
		code = "context_budget_exceeded"
	case errors.Is(resultErr, ErrSourceModelProtocol):
		code = "invalid_model_response"
	}
	text := e.runner.Result().FinalText
	return contracts.ExecutionResult{FinalText: text, ErrorCode: code}, resultErr
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
	switch e.Query.Domain {
	case "train":
		fmt.Fprintf(&b, "车次查询：%s → %s，%s。\n", e.Query.Origin, e.Query.Destination, e.Query.Date)
	case "hotel":
		fmt.Fprintf(&b, "酒店地点查询：%s，%s 至 %s。\n", e.Query.City, e.Query.CheckIn, e.Query.CheckOut)
	case "weather":
		fmt.Fprintf(&b, "天气查询：%s，%s。\n", e.Query.City, e.Query.Date)
	}
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
