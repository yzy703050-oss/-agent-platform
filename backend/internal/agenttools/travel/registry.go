package travel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	"github.com/cloudwego/eino/schema"
	js "github.com/google/jsonschema-go/jsonschema"
)

type ProfileRequest struct {
	RefreshedTripIDs []string            `json:"refreshed_trip_ids,omitempty"`
	Role             string              `json:"role"`
	PolicyObserved   bool                `json:"policy_observed"`
	AllowedTripIDs   []string            `json:"allowed_trip_ids,omitempty"`
	RetryQueries     []model.TravelQuery `json:"retry_queries,omitempty"`
}
type NativeCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type PreparedCall struct {
	NativeCall
	Rejected   *model.TravelRuleError     `json:"rejected,omitempty"`
	Normalized json.RawMessage            `json:"normalized"`
	Category   string                     `json:"category"`
	Trip       *model.TripCommand         `json:"trip,omitempty"`
	Decision   *model.TripDecisionContext `json:"decision_context,omitempty"`
	Queries    []model.TravelQuery        `json:"queries,omitempty"`
	Read       *NativeReadArguments       `json:"read,omitempty"`
	Memory     *NativeMemoryArguments     `json:"memory,omitempty"`
	Policy     *NativePolicyArguments     `json:"policy,omitempty"`
	Replan     *ReplanArguments           `json:"replan,omitempty"`
	Reply      *NativeReply               `json:"reply,omitempty"`
}
type PreparedBatch struct {
	Request   ProfileRequest `json:"request"`
	Calls     []PreparedCall `json:"calls"`
	QueryKeys []string       `json:"query_keys,omitempty"`
}
type NativeConfig struct {
	OperationID func(context.Context, string) (string, error)
	Config
	Context      func(context.Context) model.TripDecisionContext
	Now          func() time.Time
	Memory       NativeMemory
	History      NativeHistory
	SourcePolicy NativePolicy
	OnNativeTrip func(context.Context, string, model.TripResult) error
}
type Registry struct {
	cfg            NativeConfig
	validators     map[string]map[string]*js.Resolved
	tripValidators map[string]map[string]*js.Resolved
}
type tripPreparer interface {
	Prepare(context.Context, model.TravelExecution, model.TripCommand) (model.TripResult, error)
}

func NewRegistry(cfg NativeConfig) (*Registry, error) {
	if cfg.Trips == nil {
		return nil, fmt.Errorf("native travel business service required")
	}
	if _, ok := cfg.Trips.(tripPreparer); !ok {
		return nil, fmt.Errorf("native travel read-only business preflight required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &Registry{cfg: cfg, validators: map[string]map[string]*js.Resolved{}, tripValidators: map[string]map[string]*js.Resolved{}}
	for _, profile := range []string{"main", "pre_policy", "review", "review_retry", "summary"} {
		infos, err := SourceProfile(profile)
		if err != nil {
			return nil, err
		}
		r.validators[profile] = map[string]*js.Resolved{}
		for _, info := range infos {
			s, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, err
			}
			data, err := json.Marshal(s)
			if err != nil {
				return nil, err
			}
			var doc js.Schema
			if err = json.Unmarshal(data, &doc); err != nil {
				return nil, err
			}
			resolved, err := doc.Resolve(nil)
			if err != nil {
				return nil, fmt.Errorf("source schema %s/%s: %w", profile, info.Name, err)
			}
			r.validators[profile][info.Name] = resolved
			if info.Name == "write_trip" {
				r.tripValidators[profile] = map[string]*js.Resolved{}
				for _, union := range doc.Properties["tripplan"].OneOf {
					name := strings.TrimPrefix(union.Ref, "#/$defs/")
					branch := doc.Defs[name]
					if branch == nil {
						continue
					}
					operation := branch.Properties["operation"]
					if operation == nil {
						continue
					}
					values := append([]any(nil), operation.Enum...)
					if operation.Const != nil {
						values = append(values, *operation.Const)
					}
					// The authoritative oneOf is unchanged. On failure only, use
					// its discriminated branch to retain leaf validation locations.
					detail := doc.CloneSchemas()
					detail.Properties["tripplan"] = detail.Defs[name].CloneSchemas()
					specific, err := detail.Resolve(nil)
					if err != nil {
						return nil, err
					}
					for _, value := range values {
						if op, ok := value.(string); ok {
							r.tripValidators[profile][op] = specific
						}
					}
				}
			}
		}
	}
	return r, nil
}

func sourceProfileName(request ProfileRequest) (string, error) {
	switch request.Role {
	case "main":
		if request.PolicyObserved {
			return "main", nil
		}
		return "pre_policy", nil
	case "verifier":
		if len(request.RetryQueries) > 0 {
			return "review_retry", nil
		}
		return "review", nil
	case "summary":
		return "summary", nil
	default:
		return "", fmt.Errorf("unknown source role: %s", request.Role)
	}
}
func (r *Registry) Profile(request ProfileRequest) ([]*schema.ToolInfo, error) {
	name, err := sourceProfileName(request)
	if err != nil {
		return nil, err
	}
	return SourceProfile(name)
}

func (r *Registry) Preflight(ctx context.Context, request ProfileRequest, calls []NativeCall) (PreparedBatch, error) {
	batch := PreparedBatch{Request: request, Calls: make([]PreparedCall, 0, len(calls))}
	if len(calls) > 8 {
		return batch, ruleError("TOOL_LIMIT_EXCEEDED", "tool_limit_exceeded", nil)
	}
	profile, err := sourceProfileName(request)
	if err != nil {
		return batch, err
	}
	ids, targets := map[string]bool{}, map[string]bool{}
	requestKind := ""
	hasPolicy, hasWrite, hasReplan := false, false, false
	for _, call := range calls {
		if call.ID == "" || ids[call.ID] {
			return batch, ruleError("DUPLICATE_CALL_ID", "duplicate_call_id", nil)
		}
		ids[call.ID] = true
		validator, ok := r.validators[profile][call.Name]
		if !ok {
			return batch, ruleError("UNKNOWN_TOOL", "unknown_tool:"+call.Name, nil)
		}
		if len(call.Arguments) > 64000 {
			return batch, ruleError("INVALID_ARGUMENTS", "tool arguments exceed limit", nil)
		}
		normalized, err := NormalizeNativeArguments(call.Name, call.Arguments, "Asia/Shanghai", request.Role)
		if err != nil {
			return batch, err
		}
		var instance any
		if err = json.Unmarshal(normalized, &instance); err != nil {
			return batch, ruleError("INVALID_ARGUMENTS", "invalid tool JSON", nil)
		}
		if err = validator.Validate(instance); err != nil {
			if call.Name == "write_trip" {
				if root, ok := instance.(map[string]any); ok {
					if plan, ok := root["tripplan"].(map[string]any); ok {
						if op, ok := plan["operation"].(string); ok {
							if specific := r.tripValidators[profile][op]; specific != nil {
								if detail := specific.Validate(instance); detail != nil {
									return batch, nativeSchemaError(detail, specific)
								}
							}
						}
					}
				}
			}
			return batch, nativeSchemaError(err, validator)
		}
		prepared := PreparedCall{NativeCall: call, Normalized: normalized}
		var common struct {
			RequestKind string `json:"request_kind"`
		}
		_ = json.Unmarshal(normalized, &common)
		if requestKind != "" && requestKind != common.RequestKind {
			return batch, ruleError("INCONSISTENT_REQUEST_KIND", "inconsistent_request_kind", nil)
		}
		requestKind = common.RequestKind
		switch call.Name {
		case "write_trip":
			prepared.Category = "complex_write"
			var args NativeTripArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			decision := model.TripDecisionContext{Role: request.Role}
			if r.cfg.Context != nil {
				decision = r.cfg.Context(ctx)
				decision.Role = request.Role
			}
			cmd, err := businessCommand(args, decision)
			if err != nil {
				return batch, err
			}
			if request.Role == "verifier" && !slices.Contains(request.AllowedTripIDs, cmd.PlanID) {
				return batch, ruleError("REVIEW_TRIP_MISMATCH", "review_trip_mismatch", nil)
			}
			if cmd.PlanID != "" {
				if targets[cmd.PlanID] {
					return batch, ruleError("DUPLICATE_TRIP_WRITE", "one TripPlan write per batch", nil)
				}
				targets[cmd.PlanID] = true
			}
			if args.Reply != nil && (cmd.Operation != "select" && cmd.Operation != "amend" || cmd.RouteTasks != nil && len(args.Reply.Tasks) > 0) {
				return batch, ruleError("INVALID_REPLY_REFERENCE", "最终reply须用于已有计划的select/amend；路线调整使用reply={}。", nil)
			}
			if preparer, ok := r.cfg.Trips.(tripPreparer); ok {
				var preview model.TripResult
				var err error
				if accepted, ok := r.cfg.Trips.(interface {
					PrepareOperation(context.Context, model.TravelExecution, string, model.TripCommand) (model.TripResult, error)
				}); ok && r.cfg.OperationID != nil {
					var id string
					id, err = r.cfg.OperationID(ctx, call.ID)
					if err == nil {
						preview, err = accepted.PrepareOperation(ctx, r.cfg.Execution, id, cmd)
					}
				} else {
					preview, err = preparer.Prepare(ctx, r.cfg.Execution, cmd)
				}
				if err != nil {
					var rule *model.TravelRuleError
					if errors.As(err, &rule) && rule.Code == "EVIDENCE_EXPIRED" && slices.Contains(request.RefreshedTripIDs, cmd.PlanID) {
						return batch, &model.TravelRuleError{Code: "AUTOMATIC_REFRESH_LIMIT", Message: "本轮已更新过候选，未再次自动刷新。", Details: rule.Details}
					}
					return batch, err
				}
				for _, q := range preview.Queries {
					batch.QueryKeys = append(batch.QueryKeys, tripsvc.ExternalQueryKeys(q)...)
				}
			}
			prepared.Trip = &cmd
			prepared.Decision = &decision
			prepared.Reply = args.Reply
			hasWrite = hasWrite || (cmd.Operation != "pause" && cmd.Operation != "cancel")
		case "read":
			prepared.Category = "read"
			var args NativeReadArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			prepared.Read = &args
		case "query":
			prepared.Category = "query"
			var args NativeQueryArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			for _, item := range args.QueryPlan {
				q, err := businessQuery(item)
				if err != nil {
					return batch, err
				}
				if request.Role == "verifier" && !slices.ContainsFunc(request.RetryQueries, func(original model.TravelQuery) bool { return equalRetryQuery(q, original) }) {
					return batch, ruleError("REVIEW_QUERY_SCOPE", "review_query_scope", nil)
				}
				prepared.Queries = append(prepared.Queries, q)
				batch.QueryKeys = append(batch.QueryKeys, tripsvc.ExternalQueryKeys(q)...)
			}
		case "write_memory":
			prepared.Category = "simple_write"
			var args NativeMemoryArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			if args.Operation == "" {
				args.Operation = "update"
			}
			if args.Operation == "update" && len(args.Updates) == 0 || args.Operation == "reanalyze" && len(args.Updates) > 0 {
				return batch, ruleError("INVALID_ARGUMENTS", "memory update needs changes; reanalysis cannot include explicit updates", nil)
			}
			prepared.Memory = &args
		case "query_policy":
			prepared.Category = "policy"
			var args NativePolicyArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			prepared.Policy = &args
			hasPolicy = true
		case "request_replan":
			prepared.Category = "control"
			var args ReplanArguments
			if err = strictDecode(normalized, &args); err != nil {
				return batch, ruleError("INVALID_ARGUMENTS", err.Error(), nil)
			}
			for _, id := range args.TripPlanIDs {
				if !slices.Contains(request.AllowedTripIDs, id) {
					return batch, ruleError("REPLAN_TARGET_MISMATCH", "replan is limited to current TripPlans", nil)
				}
			}
			if strings.TrimSpace(args.Reason) == "" {
				return batch, ruleError("INVALID_ARGUMENTS", "replan reason is required", nil)
			}
			prepared.Replan = &args
			hasReplan = true
		}
		batch.Calls = append(batch.Calls, prepared)
	}
	if hasReplan && len(calls) != 1 {
		return batch, ruleError("REPLAN_BATCH_CONFLICT", "request_replan must be submitted alone", nil)
	}
	if hasPolicy && hasWrite {
		return batch, ruleError("POLICY_OBSERVATION_REQUIRED", "policy_observation_required", nil)
	}
	slices.Sort(batch.QueryKeys)
	batch.QueryKeys = slices.Compact(batch.QueryKeys)
	if len(batch.QueryKeys) > 12 {
		return batch, ruleError("EXTERNAL_QUERY_LIMIT_EXCEEDED", "external_query_limit_exceeded", nil)
	}
	return batch, nil
}

func ruleError(code, message string, details map[string]json.RawMessage) error {
	return &model.TravelRuleError{Code: code, Message: message, Details: details}
}
