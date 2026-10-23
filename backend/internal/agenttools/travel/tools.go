// Package travel exposes authorized business use cases as Eino tools.
package travel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agenttools"
	"agent-platform/backend/internal/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type Trips interface {
	Apply(context.Context, model.TravelExecution, string, model.TripCommand) (model.TripResult, error)
	Read(context.Context, model.Scope, string) (model.TripPlan, error)
	Query(context.Context, model.TravelExecution, model.TravelQuery) (model.TravelEvidence, error)
}
type Preferences interface {
	Apply(context.Context, model.TravelExecution, string, model.TravelPreferenceCommand) (model.TravelPreference, error)
}
type Config struct {
	Trips       Trips
	Preferences Preferences
	Execution   model.TravelExecution
	Timeout     time.Duration
	OnTrip      func(context.Context, model.TripResult) error
}
type operationKey struct{}

func WithOperationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationKey{}, id)
}
func operationID(ctx context.Context) (string, error) {
	id, _ := ctx.Value(operationKey{}).(string)
	if id == "" {
		return "", errors.New("accepted travel decision required")
	}
	return id, nil
}

type TripObservation struct {
	Trip     model.TripResult       `json:"trip"`
	Evidence []model.TravelEvidence `json:"evidence,omitempty"`
}
type readArgs struct {
	PlanID string `json:"plan_id" jsonschema:"description=已保存的行程ID"`
}

func New(cfg Config) ([]tool.BaseTool, error) {
	if cfg.Trips == nil || cfg.Preferences == nil {
		return nil, errors.New("travel services required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	read, err := infer("read_travel", "读取当前用户已保存的行程，不修改。", cfg.Timeout, func(ctx context.Context, in *readArgs) (*model.TripPlan, error) {
		p, err := cfg.Trips.Read(ctx, cfg.Execution.Scope, in.PlanID)
		return &p, err
	})
	if err != nil {
		return nil, err
	}
	query, err := infer("query_travel", "独立查询或按已保存行程查询 train/hotel/weather。引用候选必须使用返回的查询ID、结果修订和候选ID。", cfg.Timeout, func(ctx context.Context, in *model.TravelQuery) (*model.TravelEvidence, error) {
		v, err := cfg.Trips.Query(ctx, cfg.Execution, *in)
		return &v, err
	})
	if err != nil {
		return nil, err
	}
	write, err := infer("write_trip", "创建 propose、局部修正 amend 或候选选择 select。金额单位为分；单程须 round_trip=false；已有行程必须带 plan_id 和 expected_revision。查询需求自动执行，只能从真实候选选择。", cfg.Timeout, func(ctx context.Context, in *model.TripCommand) (*TripObservation, error) {
		id, err := operationID(ctx)
		if err != nil {
			return nil, err
		}
		p, err := cfg.Trips.Apply(ctx, cfg.Execution, id, *in)
		if err != nil {
			return nil, err
		}
		if cfg.OnTrip != nil {
			if err = cfg.OnTrip(ctx, p); err != nil {
				return nil, err
			}
		}
		out := &TripObservation{Trip: p}
		for _, q := range p.Queries {
			e, err := cfg.Trips.Query(ctx, cfg.Execution, q)
			if err != nil {
				return nil, err
			}
			out.Evidence = append(out.Evidence, e)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	pref, err := infer("write_travel_preference", "仅在用户明确要求长期保存或撤回时调用；operation=set/retract，key 为偏好项，value 为用户要求。", cfg.Timeout, func(ctx context.Context, in *model.TravelPreferenceCommand) (*model.TravelPreference, error) {
		id, err := operationID(ctx)
		if err != nil {
			return nil, err
		}
		in.Origin = "explicit"
		p, err := cfg.Preferences.Apply(ctx, cfg.Execution, id, *in)
		return &p, err
	})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{read, query, write, pref}, nil
}

func infer[I, O any](name, desc string, timeout time.Duration, fn func(context.Context, *I) (*O, error)) (tool.InvokableTool, error) {
	t, err := utils.InferTool(name, desc, fn, utils.WithUnmarshalArguments(func(_ context.Context, args string) (any, error) {
		if len(args) > 64000 || strings.TrimSpace(args) == "null" {
			return nil, model.ErrInvalidInput
		}
		var in I
		d := json.NewDecoder(strings.NewReader(args))
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil {
			return nil, model.ErrInvalidInput
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, model.ErrInvalidInput
		}
		return &in, nil
	}))
	if err != nil {
		return nil, err
	}
	return &observedTool{InvokableTool: t, name: name, timeout: timeout}, nil
}

type observedTool struct {
	tool.InvokableTool
	name    string
	timeout time.Duration
}

func (t *observedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.InvokableTool.Info(ctx)
}
func (t *observedTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (result string, err error) {
	started := time.Now()
	payload, _ := json.Marshal(map[string]any{"name": t.name})
	if err = agenttools.Emit(ctx, contracts.Event{Type: "tool.started", Payload: payload}); err != nil {
		return "", err
	}
	defer func() {
		kind := "tool.completed"
		if err != nil {
			kind = "tool.failed"
		}
		b, _ := json.Marshal(map[string]any{"name": t.name, "duration_ms": time.Since(started).Milliseconds()})
		if e := agenttools.Emit(ctx, contracts.Event{Type: kind, Payload: b}); e != nil && err == nil {
			result = ""
			err = e
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	result, err = t.InvokableTool.InvokableRun(bounded, args, opts...)
	if bounded.Err() != nil {
		return "", agenttools.ErrToolTimeout
	}
	if err != nil && (errors.Is(err, model.ErrInvalidInput) || errors.Is(err, model.ErrNotFound)) {
		// Correctable business observations contain no database or provider details.
		return `{"status":"invalid_input","message":"参数或候选无效，请根据已返回的条件和证据修正"}`, nil
	}
	return result, err
}
