package travel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	traveltools "agent-platform/backend/internal/agenttools/travel"
	pm "agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/schema"
)

// FlowState belongs to this Travel execution, not to the platform Run or a
// business TripPlan. Every field needed by the next decision is serializable.
type FlowState struct {
	FinalText            string                   `json:"final_text,omitempty"`
	AuxiliaryErrors      []string                 `json:"auxiliary_errors,omitempty"`
	RefreshedPlans       map[string]bool          `json:"refreshed_plans,omitempty"`
	ExternalQueryKeys    []string                 `json:"external_query_keys,omitempty"`
	PolicyUserContext    pm.TripConditions        `json:"policy_user_context,omitempty"`
	HistoryInitialized   bool                     `json:"history_initialized"`
	PreferenceMaintained bool                     `json:"preference_maintained"`
	HistorySummary       string                   `json:"history_summary,omitempty"`
	SummarySourceRuns    []string                 `json:"summary_source_runs,omitempty"`
	SummaryVersion       int64                    `json:"summary_version,omitempty"`
	PreparedModelKey     int                      `json:"prepared_model_key"`
	PreparedMessages     []*schema.Message        `json:"prepared_messages,omitempty"`
	PlanningIntent       bool                     `json:"planning_intent"`
	Version              int                      `json:"version"`
	Phase                string                   `json:"phase"`
	Role                 string                   `json:"role"`
	OriginalQuery        string                   `json:"original_query"`
	Planning             bool                     `json:"planning_started"`
	PlanningRounds       int                      `json:"planning_rounds_used"`
	ActiveNanos          int64                    `json:"active_nanos"`
	StopReason           string                   `json:"stop_reason,omitempty"`
	ReviewFingerprint    string                   `json:"review_fingerprint,omitempty"`
	UnchangedReviews     int                      `json:"unchanged_reviews"`
	Plans                map[string]pm.TripResult `json:"plans"`
	Jobs                 []traveltools.QueryJob   `json:"query_jobs"`
	Results              []pm.TravelToolResult    `json:"results"`
	RawResults           []json.RawMessage        `json:"raw_results"`
	PreviousRoundSummary string                   `json:"previous_round_summary"`
	AdjustmentHistory    []json.RawMessage        `json:"adjustment_history"`
	Policy               *pm.SourcePolicyContext  `json:"policy_context,omitempty"`
	Preferences          map[string]string        `json:"preferences"`
	RetryQueries         []pm.TravelQuery         `json:"retry_queries"`
	BlockedEvidence      []pm.TravelCandidateRef  `json:"blocked_evidence"`
	GenerationFailures   int                      `json:"generation_failures"`
}

func (s FlowState) DecideRole() string {
	if len(s.Plans) > 0 {
		role := "verifier"
		for _, result := range s.Results {
			if result.Name == "request_replan" && result.Status == "ok" {
				role = "main"
			}
			if result.Name == "write_trip" && (result.Status == "completed" || result.Status == "partial") && result.TripPlanID != "" {
				role = "verifier"
			}
		}
		if len(s.Results) == 0 {
			return "main"
		}
		return role
	}
	if len(s.Results) == 0 || s.Planning || s.PlanningIntent {
		return "main"
	}
	return "summary"
}

func (s *FlowState) ReserveBatch(calls []traveltools.NativeCall, limit int) error {
	s.ExternalQueryKeys = nil
	planning := s.Planning
	for _, call := range calls {
		var args struct {
			RequestKind string `json:"request_kind"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		if args.RequestKind == "planning" {
			s.PlanningIntent = true
		}
		if call.Name == "write_trip" {
			planning = true
		}
	}
	if planning && s.PlanningRounds >= limit {
		return &pm.TravelRuleError{Code: "PLANNING_ROUND_LIMIT_EXCEEDED", Message: "planning_round_limit_exceeded"}
	}
	if planning {
		s.PlanningRounds++
	}
	s.Planning = planning
	return nil
}

func (s FlowState) planIDs() []string {
	ids := make([]string, 0, len(s.Plans))
	for id := range s.Plans {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (s *FlowState) ObserveReview() {
	states := []any{}
	for _, id := range s.planIDs() {
		plan := s.Plans[id]
		view := s.PlanView(plan)
		tasks := []any{}
		for _, value := range view["tasks"].([]any) {
			row := value.(map[string]any)
			current := map[string]any{}
			for _, key := range []string{"id", "origin", "destination", "purpose", "requires_hotel", "conditions", "field_sources", "condition_strengths", "content", "status"} {
				current[key] = row[key]
			}
			draft := map[string]any{}
			if d, ok := row["draft_plan"].(map[string]any); ok {
				for key, value := range d {
					if key != "task_revision" {
						draft[key] = value
					}
				}
			}
			current["draft_plan"] = draft
			tasks = append(tasks, current)
		}
		issues := []any{}
		for _, issue := range plan.Validation.Issues {
			issues = append(issues, map[string]any{"code": issue.Code, "task_ids": issue.TaskIDs, "blocking": issue.Blocking, "evidence": issue.Evidence})
		}
		states = append(states, map[string]any{"trip_plan_id": id, "tasks": tasks, "conditions": view["confirmed_conditions"], "policy": plan.Plan.PolicyContext, "candidates": view["candidates"], "pending_queries": view["pending_queries"], "issues": issues})
	}
	raw, _ := json.Marshal(states)
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	if fingerprint == s.ReviewFingerprint {
		s.UnchangedReviews++
	} else {
		s.UnchangedReviews = 0
	}
	s.ReviewFingerprint = fingerprint
	if s.UnchangedReviews >= 2 && s.StopReason == "" {
		s.StopReason = "no_progress"
		s.Phase = "stopping"
	}
}

func (s FlowState) PlanView(result pm.TripResult) map[string]any {
	view := traveltools.NativeTripView(result)
	p := result.Plan
	conditions := p.ConfirmedConditions
	if conditions == nil {
		conditions = pm.TripConditions{}
	}
	sources := p.FieldSources
	if sources == nil {
		sources = map[string]string{}
	}
	strengths := p.ConditionStrengths
	if strengths == nil {
		strengths = map[string]pm.ConstraintStrength{}
	}
	view["confirmed_conditions"] = conditions
	view["field_sources"] = sources
	view["condition_strengths"] = strengths
	view["policy_context"] = p.PolicyContext
	view["constraint_snapshot"] = p.ConstraintSnapshot
	candidates := []any{}
	queries := map[string]int64{}
	pending := []any{}
	for _, job := range s.queryViews(p) {
		if job.Query.PlanID != p.ID || job.Result == nil {
			continue
		}
		taskIndex := slices.IndexFunc(p.Tasks, func(t pm.TripTask) bool {
			return t.ID == job.Query.TaskID && max(1, t.Revision) == job.Query.TaskRevision
		})
		if taskIndex < 0 {
			continue
		}
		e := *job.Result
		candidateView := traveltools.NativeEvidenceView(e)
		candidateView["task_id"] = job.Query.TaskID
		candidateView["task_revision"] = job.Query.TaskRevision
		if e.ID != "" {
			queries[e.ID] = e.ResultRevision
		}
		candidates = append(candidates, candidateView)
	}
	for _, q := range result.Queries {
		done := false
		for _, job := range s.Jobs {
			if job.Result != nil && job.Query.PlanID == q.PlanID && job.Query.TaskID == q.TaskID && job.Query.TaskRevision == q.TaskRevision && job.Query.Domain == q.Domain {
				done = true
			}
		}
		if !done {
			pending = append(pending, map[string]any{"task_id": q.TaskID, "domain": q.Domain, "reason": "awaiting_query"})
		}
	}
	view["candidates"] = candidates
	view["candidate_queries"] = queries
	view["pending_queries"] = pending
	return view
}

func (s FlowState) queryViews(p pm.TripPlan) []traveltools.QueryJob {
	rows := []traveltools.QueryJob{}
	keys := slices.Clone(p.QueryOrder)
	if len(keys) == 0 {
		for key := range p.QueryResults {
			keys = append(keys, key)
		}
		slices.Sort(keys)
	}
	for _, key := range keys {
		if e, ok := p.QueryResults[key]; ok {
			rows = append(rows, traveltools.QueryJob{Query: e.Query, Result: &e})
		}
	}
	for _, job := range s.Jobs {
		if job.Query.PlanID == p.ID && job.Result != nil && !slices.ContainsFunc(rows, func(old traveltools.QueryJob) bool { return old.Result.ID != "" && old.Result.ID == job.Result.ID }) {
			rows = append(rows, job)
		}
	}
	rows = slices.DeleteFunc(rows, func(job traveltools.QueryJob) bool {
		return slices.ContainsFunc(s.BlockedEvidence, func(ref pm.TravelCandidateRef) bool {
			return ref.QueryID == job.Result.ID && ref.ResultRevision == job.Result.ResultRevision
		})
	})
	return rows
}

func ReadyReply(batch traveltools.PreparedBatch, results []pm.TravelToolResult) string {
	byCall := map[string]pm.TravelToolResult{}
	accepted := map[string]bool{}
	for _, r := range results {
		byCall[r.CallID] = r
		if r.Name != "write_trip" || !slices.Contains([]string{"select", "amend"}, r.Operation) || r.TripPlanID == "" || r.Revision <= 0 {
			continue
		}
		if r.Status == "completed" {
			accepted[r.CallID] = true
			continue
		}
		if r.Status != "partial" || !r.StopPlanning || r.Validation == nil {
			continue
		}
		valid, policyOnly, blocking := true, true, 0
		for _, issue := range r.Validation.Issues {
			if issue.Blocking {
				blocking++
				if !slices.Contains([]string{"policy_unverified", "policy_constraint_unverified", "hotel_price_unknown", "total_budget_unknown"}, issue.Code) {
					valid = false
				}
				if !slices.Contains([]string{"policy_unverified", "policy_constraint_unverified"}, issue.Code) {
					policyOnly = false
				}
			}
		}
		allow := false
		for _, call := range batch.Calls {
			if call.ID == r.CallID && call.Trip != nil && call.Trip.AllowRepair != nil && !*call.Trip.AllowRepair {
				allow = true
			}
		}
		accepted[r.CallID] = valid && (allow || r.StopReason == "policy_unverified" && blocking > 0 && policyOnly)
	}
	for _, r := range results {
		if !accepted[r.CallID] && r.Status != "ok" && r.Status != "completed" {
			return ""
		}
	}
	answers := []string{}
	for _, call := range batch.Calls {
		r, ok := byCall[call.ID]
		if !ok {
			return ""
		}
		if accepted[call.ID] && r.RenderedAnswer != "" && !slices.Contains(answers, r.RenderedAnswer) {
			answers = append(answers, r.RenderedAnswer)
		}
	}
	return strings.Join(answers, "\n\n")
}
