package travel

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"agent-platform/backend/internal/agent/memory"
	"agent-platform/backend/internal/agent/ports"
	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const flowKey = "platform.travel.source-flow.v1"

type flowConfig struct {
	Decisions       ports.TravelDecisionStore
	Preference      *PreferenceAgent
	ContextTokens   int
	History         []pm.Message
	Model           em.BaseChatModel
	Summaries       memory.SummaryStore
	ReadPreferences func(context.Context) ([]pm.TravelPreference, error)
	Trips           traveltools.Trips
	Execution       pm.TravelExecution
	OriginalQuery   string
	InitialPlanIDs  []string
	PlanningRounds  int
	Timeout         time.Duration
	Now             func() time.Time
	Snapshot        func(FlowState)
}
type sourceFlow struct {
	mu       sync.Mutex
	cfg      flowConfig
	registry *traveltools.Registry
	lastTick time.Time
}

func newSourceFlow(cfg flowConfig) *sourceFlow {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PlanningRounds <= 0 {
		cfg.PlanningRounds = 8
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 600 * time.Second
	}
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = 20000
	}
	return &sourceFlow{cfg: cfg}
}

func (f *sourceFlow) load(ctx context.Context) (FlowState, error) {
	s := FlowState{Version: 1, Phase: "deciding", Role: "main", OriginalQuery: f.cfg.OriginalQuery, Plans: map[string]pm.TripResult{}, Preferences: map[string]string{}}
	if value, ok := adk.GetSessionValue(ctx, flowKey); ok {
		text, ok := value.(string)
		if !ok || json.Unmarshal([]byte(text), &s) != nil || s.Version != 1 || s.PlanningRounds < 0 || s.ActiveNanos < 0 || !slices.Contains([]string{"main", "verifier", "summary"}, s.Role) {
			return FlowState{}, errors.New("incompatible source travel flow state")
		}
		if !knownPhase(s.Phase) {
			return FlowState{}, &pm.TravelRuleError{Code: "INVALID_EXECUTION_STATE", Message: "unknown execution phase"}
		}
	}
	if s.Plans == nil {
		s.Plans = map[string]pm.TripResult{}
	}
	if s.Preferences == nil {
		s.Preferences = map[string]string{}
	}
	return s, nil
}
func (f *sourceFlow) save(ctx context.Context, s FlowState) error {
	if !knownPhase(s.Phase) {
		return &pm.TravelRuleError{Code: "INVALID_EXECUTION_STATE", Message: "unknown execution phase"}
	}
	now := f.cfg.Now()
	if !f.lastTick.IsZero() {
		s.ActiveNanos += max(int64(0), int64(now.Sub(f.lastTick)))
	}
	f.lastTick = now
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	adk.AddSessionValue(ctx, flowKey, string(raw))
	host, err := readState(ctx)
	if err != nil {
		return err
	}
	host.Phase = PhasePlanning
	if s.Role == "verifier" {
		host.Phase = PhaseValidating
	}
	if slices.ContainsFunc(s.Jobs, func(job traveltools.QueryJob) bool { return job.Result == nil }) {
		host.Phase = PhaseQuerying
	}
	if slices.Contains([]string{"stopping", "reply_ready", "completed", "stopped"}, s.Phase) {
		host.Phase = PhaseReply
	}
	if len(s.Plans) > 0 {
		host.PlanID = s.planIDs()[0]
	}
	if err = saveState(ctx, host); err != nil {
		return err
	}
	if f.cfg.Snapshot != nil {
		f.cfg.Snapshot(s)
	}
	return nil
}
func (f *sourceFlow) profile(ctx context.Context) (traveltools.ProfileRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return traveltools.ProfileRequest{}, err
	}
	role := s.Role
	if role == "" {
		role = s.DecideRole()
	}
	if role == "summary" {
		role = "main"
	} // Prompt role and execution authority differ in the original loop.
	if s.StopReason != "" || s.summaryOnly() {
		role = "summary"
	}
	request := profileForState(s, role)
	for id, refreshed := range s.RefreshedPlans {
		if refreshed {
			request.RefreshedTripIDs = append(request.RefreshedTripIDs, id)
		}
	}
	slices.Sort(request.RefreshedTripIDs)
	return request, nil
}
func (f *sourceFlow) decision(ctx context.Context) pm.TripDecisionContext {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return pm.TripDecisionContext{}
	}
	refs := []pm.TravelCandidateRef{}
	for _, job := range s.Jobs {
		if job.Result != nil && job.Result.ID != "" && !slices.ContainsFunc(s.BlockedEvidence, func(ref pm.TravelCandidateRef) bool {
			return ref.QueryID == job.Result.ID && ref.ResultRevision == job.Result.ResultRevision
		}) {
			refs = append(refs, pm.TravelCandidateRef{QueryID: job.Result.ID, ResultRevision: job.Result.ResultRevision})
		}
	}
	role := s.Role
	if role == "summary" {
		role = "main"
	}
	return pm.TripDecisionContext{Role: role, OriginalQuery: s.OriginalQuery, Policy: s.Policy, PolicyScope: s.PolicyUserContext, EvidenceRefs: refs, Preferences: s.Preferences}
}
func (f *sourceFlow) reserve(ctx context.Context, calls []traveltools.NativeCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return err
	}
	if s.StopReason != "" || s.summaryOnly() {
		return &pm.TravelRuleError{Code: "EXECUTION_STOPPED", Message: s.StopReason}
	}
	// Rejected batches are observed in afterBatch before choosing a stop phase.
	err = s.ReserveBatch(calls, f.cfg.PlanningRounds)
	if err != nil {
		return err
	}
	if err = s.transition("executing"); err != nil {
		return err
	}
	if saved := f.save(ctx, s); saved != nil {
		return saved
	}
	return nil
}

func (f *sourceFlow) modelObserved(ctx context.Context, msg *schema.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return err
	}
	// WrapModel can finalize a plain response before Eino's AfterModel hook.
	// Observing that same saved response is idempotent, never a terminal exit.
	if (s.Phase == "completed" || s.Phase == "stopped") && s.FinalText != "" && len(msg.ToolCalls) == 0 && msg.Content == s.FinalText {
		return nil
	}
	if len(msg.ToolCalls) > 0 {
		// A model ignoring summary-only tools still gets paired rejected results.
		if !s.summaryOnly() {
			if err = s.transition("executing"); err != nil {
				return err
			}
		}
	} else {
		for _, p := range s.Plans {
			if p.Validation.Status != "complete" && s.StopReason == "" {
				if err = s.stop("uncommitted_plan"); err != nil {
					return err
				}
			}
		}
		if err = s.transition("reply_ready"); err != nil {
			return err
		}
	}
	return f.save(ctx, s)
}
func (f *sourceFlow) recordTrip(ctx context.Context, callID string, result pm.TripResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return err
	}
	jobs, err := traveltools.PlanQueryJobs(result.Receipt.OperationID, callID, result)
	if err != nil {
		return err
	}
	s.Plans[result.Plan.ID] = result
	for _, job := range jobs {
		if !slices.ContainsFunc(s.Jobs, func(old traveltools.QueryJob) bool { return old.ID == job.ID }) {
			s.Jobs = append(s.Jobs, job)
		}
	}
	return f.save(ctx, s)
}
func (f *sourceFlow) recordJob(ctx context.Context, job traveltools.QueryJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(s.Jobs, func(old traveltools.QueryJob) bool { return old.ID == job.ID })
	if index < 0 {
		return pm.ErrIdempotencyConflict
	}
	s.Jobs[index] = job
	return f.save(ctx, s)
}
func (f *sourceFlow) runJobs(ctx context.Context) error {
	f.mu.Lock()
	s, err := f.load(ctx)
	if err == nil && s.summaryOnly() {
		f.mu.Unlock()
		return nil
	}
	if err == nil {
		if reader, ok := f.cfg.Trips.(interface {
			ListQueryJobs(context.Context, pm.TravelExecution) ([]pm.TravelQueryJob, error)
		}); ok {
			var stored []pm.TravelQueryJob
			stored, err = reader.ListQueryJobs(ctx, f.cfg.Execution)
			for _, job := range stored {
				index := slices.IndexFunc(s.Jobs, func(old traveltools.QueryJob) bool { return old.ID == job.ID })
				if index < 0 {
					s.Jobs = append(s.Jobs, job)
				} else if job.Result != nil {
					s.Jobs[index].Result = job.Result
				}
			}
			if err == nil {
				err = f.save(ctx, s)
			}
		}
	}
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if len(s.Jobs) == 0 {
		return nil
	}
	_, err = f.registry.ExecuteQueryJobs(ctx, s.Jobs, f.recordJob)
	return err
}

func (f *sourceFlow) beforeModel(ctx context.Context, history []*schema.Message) ([]*schema.Message, string, error) {
	if f.cfg.Decisions != nil {
		host, err := readState(ctx)
		if err != nil {
			return nil, "", err
		}
		d, ok, err := f.cfg.Decisions.FindTravelDecision(ctx, f.cfg.Execution, decisionKey(host.ModelCalls))
		if err != nil {
			return nil, "", err
		}
		if ok {
			if len(d.FlowSnapshot) == 0 || len(d.InputMessages) == 0 {
				return nil, "", pm.ErrIdempotencyConflict
			}
			f.mu.Lock()
			adk.AddSessionValue(ctx, flowKey, string(d.FlowSnapshot))
			s, err := f.load(ctx)
			if err == nil {
				err = f.save(ctx, s)
			}
			f.mu.Unlock()
			if err != nil {
				return nil, "", err
			}
			messages := make([]*schema.Message, 0, len(d.InputMessages))
			for _, m := range d.InputMessages {
				messages = append(messages, toEino(m))
			}
			return messages, "", nil
		}
	}
	if err := f.runJobs(ctx); err != nil {
		return nil, "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return nil, "", err
	}
	if slices.Contains([]string{"completed", "stopped", "paused"}, s.Phase) {
		if s.Phase != "paused" && s.FinalText != "" {
			return history, s.FinalText, nil
		}
		return nil, "", &pm.TravelRuleError{Code: "EXECUTION_STOPPED", Message: "execution no longer permits a model decision"}
	}
	host, err := readState(ctx)
	if err != nil {
		return nil, "", err
	}
	if f.cfg.Decisions == nil && len(s.PreparedMessages) > 0 && s.PreparedModelKey == host.ModelCalls {
		return s.PreparedMessages, "", nil
	}
	if !s.PreferenceMaintained && f.cfg.Preference != nil {
		if err := f.cfg.Preference.Maintain(ctx, f.cfg.Execution); err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			s.AuxiliaryErrors = append(s.AuxiliaryErrors, "preference_analysis_error")
		}
		s.PreferenceMaintained = true
	}
	if len(s.Plans) == 0 && len(s.Results) == 0 {
		for _, id := range f.cfg.InitialPlanIDs {
			p, err := f.cfg.Trips.Inspect(ctx, f.cfg.Execution, id)
			if err != nil {
				return nil, "", err
			}
			s.Plans[id] = p
		}
	}
	// Re-read saved plans; the execution state never outranks business receipts.
	for _, id := range s.planIDs() {
		current, err := f.cfg.Trips.Inspect(ctx, f.cfg.Execution, id)
		if err != nil {
			return nil, "", err
		}
		old := s.Plans[id]
		current.Receipt = old.Receipt
		current.Operation = old.Operation
		s.Plans[id] = current
	}
	if !s.HistoryInitialized {
		s.AdjustmentHistory = append(sourceAdjustmentHistory(f.cfg.History, s.Plans), s.AdjustmentHistory...)
		s.HistoryInitialized = true
	}
	s.Role = s.DecideRole()
	queued, err := f.refreshBeforeReview(ctx, &s)
	if err != nil {
		return nil, "", err
	}
	if queued {
		if err = f.save(ctx, s); err != nil {
			return nil, "", err
		}
		f.mu.Unlock()
		err = f.runJobs(ctx)
		f.mu.Lock()
		if err != nil {
			return nil, "", err
		}
		s, err = f.load(ctx)
		if err != nil {
			return nil, "", err
		}
		for _, id := range s.planIDs() {
			current, err := f.cfg.Trips.Inspect(ctx, f.cfg.Execution, id)
			if err != nil {
				return nil, "", err
			}
			old := s.Plans[id]
			current.Receipt = old.Receipt
			current.Operation = old.Operation
			s.Plans[id] = current
		}
	}
	if failedSourceRefresh(s) && s.StopReason == "" {
		if err = s.stop("automatic_refresh_failed"); err != nil {
			return nil, "", err
		}
	}
	memories := []any{}
	preferenceSummary := ""
	if f.cfg.ReadPreferences != nil {
		preferences, err := f.cfg.ReadPreferences(ctx)
		if err != nil {
			return nil, "", err
		}
		s.Preferences = map[string]string{}
		for _, p := range preferences {
			if p.Retracted {
				continue
			}
			if p.Key == "$analysis" {
				var analysis pm.TravelPreferenceAnalysis
				if json.Unmarshal([]byte(p.Value), &analysis) == nil {
					preferenceSummary = analysis.Summary
				}
			}
			if strings.HasPrefix(p.Key, "memory_") {
				source := "explicit"
				if p.Origin == "inferred" {
					source = "history"
				}
				memories = append(memories, map[string]any{"id": p.Key, "text": p.Value, "source": source, "status": "active"})
			} else if !strings.HasPrefix(p.Key, "$") {
				s.Preferences[p.Key] = p.Value
			}
		}
	}
	if s.Planning && s.PlanningRounds >= f.cfg.PlanningRounds && s.StopReason == "" {
		if err = s.stop("planning_round_limit_exceeded"); err != nil {
			return nil, "", err
		}
	}
	if time.Duration(s.ActiveNanos) >= f.cfg.Timeout && s.StopReason == "" {
		if err = s.stop("turn_deadline_exceeded"); err != nil {
			return nil, "", err
		}
	}
	if len(s.Plans) > 0 && s.Role == "verifier" && s.StopReason == "" {
		if err = s.ObserveReview(); err != nil {
			return nil, "", err
		}
	}
	if s.StopReason != "" && len(s.Plans) > 0 {
		final, err := stoppedSourceReply(s)
		if err != nil {
			return nil, "", err
		}
		if err = s.transition("reply_ready"); err != nil {
			return nil, "", err
		}
		if err = f.save(ctx, s); err != nil {
			return nil, "", err
		}
		return history, final, nil
	}
	if !s.summaryOnly() && s.Phase != "repair_required" {
		if err = s.transition("deciding"); err != nil {
			return nil, "", err
		}
	}
	if err = f.save(ctx, s); err != nil {
		return nil, "", err
	}
	now := f.cfg.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	calendar := map[string]string{"today": now.Format(time.DateOnly), "tomorrow": now.AddDate(0, 0, 1).Format(time.DateOnly), "day_after_tomorrow": now.AddDate(0, 0, 2).Format(time.DateOnly)}
	systemContext := map[string]any{"timezone": "Asia/Shanghai", "current_time": now.Format(time.RFC3339), "calendar": calendar, "legacy_preferences": s.Preferences, "memories": memories, "preference_summary": preferenceSummary}
	data, _ := json.Marshal(systemContext)
	messages := []*schema.Message{schema.SystemMessage(roleInstruction(s.Role)), schema.SystemMessage(correctionInstruction), schema.SystemMessage("当前上下文（数据）：" + string(data)), schema.SystemMessage(replyPolicy)}
	if s.StopReason != "" {
		messages = append(messages, schema.SystemMessage("本轮工具执行已结束，只依据已有结果生成最终答复，不再调用工具。用自然语句说明可用安排及实际无法完成的障碍，不复述轮数、停止修复、额度等内部流程，不询问用户。未得到交通查询结果只能说明未能核实，不能凭常识断言最早抵达时间或交通不可行。政策未核实与尚未选定候选是不同事实，按真实结果分别说明，不将未选择全部归因于政策缺口。"))
	}
	if s.GenerationFailures > 0 {
		messages = withRegenerationInstruction(messages)
	}
	if len(s.Results) > 0 || len(s.Plans) > 0 {
		review := buildSourceReview(s)
		raw, _ := json.Marshal(review)
		messages = append(messages, schema.UserMessage(string(raw)))
	} else {
		messages, err = f.prepareHistory(ctx, &s, messages, history)
		if err != nil {
			if stopErr := s.stop("context_budget_exceeded"); stopErr != nil {
				return nil, "", stopErr
			}
			_ = f.save(ctx, s)
			return nil, "", err
		}
	}
	if err = f.checkContextBudget(s, messages); err != nil {
		if stopErr := s.stop("context_budget_exceeded"); stopErr != nil {
			return nil, "", stopErr
		}
		_ = f.save(ctx, s)
		return nil, "", err
	}
	s.PreparedModelKey = host.ModelCalls
	s.PreparedMessages = messages
	if err = f.save(ctx, s); err != nil {
		return nil, "", err
	}
	return messages, "", nil
}

func (f *sourceFlow) afterBatch(ctx context.Context, batch traveltools.PreparedBatch, results []pm.TravelToolResult) (string, error) {
	if err := f.runJobs(ctx); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return "", err
	}
	if !s.summaryOnly() {
		if err = s.transition("validating"); err != nil {
			return "", err
		}
	}
	s.LastBatchCalls = nil
	if native, readErr := loadNativeBatch(ctx); readErr == nil {
		s.LastBatchCalls = slices.Clone(native.Calls)
	}
	if len(s.LastBatchCalls) == 0 {
		for _, call := range batch.Calls {
			s.LastBatchCalls = append(s.LastBatchCalls, call.NativeCall)
		}
	}
	s.LastBatchResults = slices.Clone(results)
	s.ExternalQueryKeys = slices.Clone(batch.QueryKeys)
	summaries := []string{}
	for _, call := range s.LastBatchCalls {
		var fields struct {
			RoundSummary string `json:"round_summary"`
		}
		_ = json.Unmarshal(call.Arguments, &fields)
		if fields.RoundSummary != "" {
			summaries = append(summaries, fields.RoundSummary)
		}
	}
	s.PreviousRoundSummary = strings.Join(summaries, "；")
	for _, r := range results {
		s.Results = append(s.Results, r)
		raw := r.Raw
		if len(raw) == 0 {
			raw, _ = json.Marshal(r)
		}
		s.RawResults = append(s.RawResults, raw)
		if r.Name == "write_trip" {
			for _, call := range batch.Calls {
				if call.ID == r.CallID {
					args := sourceObject(call.Arguments)
					changes, _ := args["tripplan"].(map[string]any)
					changes = sourceChanges(changes)
					s.AdjustmentHistory = append(s.AdjustmentHistory, mustMarshal(map[string]any{"call_id": r.CallID, "trip_plan_id": r.TripPlanID, "operation": r.Operation, "status": r.Status, "revision": r.Revision, "summary": s.PreviousRoundSummary, "changes": changes, "effect": map[bool]string{true: "saved_draft", false: "not_applied"}[r.TripPlanID != ""]}))
				}
			}
		}
		if r.Name == "query_policy" && (r.Status == "ok" || r.Status == "partial") {
			var policy pm.SourcePolicyContext
			if json.Unmarshal(r.Data, &policy) == nil && policy.Status != "" {
				s.Policy = &policy
				for _, call := range batch.Calls {
					if call.ID == r.CallID && call.Policy != nil {
						s.PolicyUserContext = pm.TripConditions(call.Policy.Context)
					}
				}
			}
		}
		if r.Name == "query" && (r.Status == "partial" || r.Status == "error") {
			for _, call := range batch.Calls {
				if call.ID == r.CallID {
					s.RetryQueries = append(s.RetryQueries, call.Queries...)
				}
			}
		}
		if r.StopPlanning && s.StopReason == "" {
			reason := r.StopReason
			if reason == "" {
				reason = "tool_requested_stop"
			}
			if err = s.stop(reason); err != nil {
				return "", err
			}
		}
		if r.Error != nil && r.Error.Code == "PLANNING_ROUND_LIMIT_EXCEEDED" {
			if err = s.stop("planning_round_limit_exceeded"); err != nil {
				return "", err
			}
		}
		if r.Error != nil && r.Error.Code == "EVIDENCE_EXPIRED" {
			var id string
			_ = json.Unmarshal(r.Error.Details["trip_plan_id"], &id)
			if id != "" {
				var queryID string
				var revision int64
				_ = json.Unmarshal(r.Error.Details["query_id"], &queryID)
				_ = json.Unmarshal(r.Error.Details["result_revision"], &revision)
				ref := pm.TravelCandidateRef{QueryID: queryID, ResultRevision: revision}
				if queryID != "" && !slices.Contains(s.BlockedEvidence, ref) {
					s.BlockedEvidence = append(s.BlockedEvidence, ref)
				}
				if _, err = f.enqueueRefresh(ctx, &s, id); err != nil {
					return "", err
				}
			}
		}
		if r.Error != nil && r.Error.Code == "AUTOMATIC_REFRESH_LIMIT" {
			if err = s.stop("automatic_refresh_limit"); err != nil {
				return "", err
			}
		}
	}
	final := ReadyReply(batch, results)
	if final != "" {
		err = s.transition("reply_ready")
	} else if s.StopReason != "" {
		err = s.stop(s.StopReason)
	} else if slices.ContainsFunc(results, func(r pm.TravelToolResult) bool {
		return r.Status == "error" || r.Status == "rejected" || r.Status == "unknown"
	}) {
		err = s.transition("repair_required")
	} else {
		err = s.transition("deciding")
	}
	if err != nil {
		return "", err
	}
	return final, f.save(ctx, s)
}

func mustMarshal(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func stoppedSourceReply(s FlowState) (string, error) {
	parts := []string{}
	for _, id := range s.planIDs() {
		p := s.Plans[id]
		text, err := tripsvc.RenderReply(p.Plan, p.Validation, pm.TripReplySpec{})
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n"), nil
}
