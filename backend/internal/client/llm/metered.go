package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"math/rand/v2"
	"sort"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/permit"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type metered struct {
	base      em.ToolCallingChatModel
	store     runtimeports.AttemptStore
	permits   permit.Service
	resources []string
}

type savedModelOutput struct {
	SchemaVersion int               `json:"schema_version"`
	Fingerprint   string            `json:"fingerprint"`
	AttemptID     string            `json:"attempt_id"`
	Ref           runtimev1.ExecRef `json:"ref"`
	Message       *schema.Message   `json:"message"`
}

func NewMetered(base em.ToolCallingChatModel, store runtimeports.AttemptStore, permits permit.Service, resources []string) em.ToolCallingChatModel {
	keys := append([]string(nil), resources...)
	sort.Strings(keys)
	return &metered{base: base, store: store, permits: permits, resources: keys}
}

func (m *metered) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	base, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	copy := *m
	copy.base = base
	return &copy, nil
}

// Only common Eino options are forwarded: implementation-specific setters
// cannot silently change the wire request outside its persisted fingerprint.
func (m *metered) options(proof *runtimeModel, limit runtimev1.LimitSnapshot, opts []em.Option) ([]em.Option, any, error) {
	common := em.GetCommonOptions(nil, opts...)
	if common.Model != nil && *common.Model != proof.config.ModelID {
		return nil, nil, model.ErrInvalidInput
	}
	if common.DeferredTools != nil || common.ToolSearchTool != nil || common.AgenticToolChoice != nil {
		return nil, nil, model.ErrInvalidInput
	}
	max := proof.config.MaxOutputTokens
	if limit.MaxModelOutputTokens < max {
		max = limit.MaxModelOutputTokens
	}
	if common.MaxTokens != nil && int64(*common.MaxTokens) < max {
		max = int64(*common.MaxTokens)
	}
	if max <= 0 || proof.config.InputTokenUpperBound > limit.MaxModelInputTokens {
		return nil, nil, model.ErrInvalidInput
	}
	tools := proof.tools
	if common.Tools != nil {
		tools = common.Tools
	}
	wire := []em.Option{em.WithMaxTokens(int(max))}
	if common.Temperature != nil {
		wire = append(wire, em.WithTemperature(*common.Temperature))
	}
	if common.TopP != nil {
		wire = append(wire, em.WithTopP(*common.TopP))
	}
	if common.Stop != nil {
		wire = append(wire, em.WithStop(common.Stop))
	}
	if common.Tools != nil {
		wire = append(wire, em.WithTools(common.Tools))
	}
	if common.ToolChoice != nil {
		wire = append(wire, em.WithToolChoice(*common.ToolChoice, common.AllowedToolNames...))
	}
	toolJSON := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return nil, nil, model.ErrInvalidInput
		}
		var params any
		if tool.ParamsOneOf != nil {
			var err error
			params, err = tool.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, nil, err
			}
		}
		toolJSON = append(toolJSON, map[string]any{"name": tool.Name, "description": tool.Desc, "parameters": params})
	}
	common.Tools = nil
	common.MaxTokens = new(int)
	*common.MaxTokens = int(max)
	return wire, map[string]any{"options": common, "tools": toolJSON}, nil
}

func cost(input, output, inputPrice, outputPrice int64) (int64, error) {
	if input < 0 || output < 0 {
		return 0, model.ErrInvalidInput
	}
	total := new(big.Int)
	for _, pair := range [][2]int64{{input, inputPrice}, {output, outputPrice}} {
		part := new(big.Int).Mul(big.NewInt(pair[0]), big.NewInt(pair[1]))
		part.Add(part, big.NewInt(999999))
		part.Div(part, big.NewInt(1000000))
		total.Add(total, part)
	}
	if !total.IsInt64() {
		return 0, model.ErrInvalidInput
	}
	return total.Int64(), nil
}

func (m *metered) usage(proof *runtimeModel, execution runtimeports.Execution, message *schema.Message) *control.Reservation {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return nil
	}
	u := message.ResponseMeta.Usage
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		return nil
	}
	c := int64(0)
	if execution.Limits.CostUpperMicros > 0 {
		var err error
		c, err = cost(int64(u.PromptTokens), int64(u.CompletionTokens), proof.config.InputMicrosPerMillion, proof.config.OutputMicrosPerMillion)
		if err != nil {
			return nil
		}
	}
	return &control.Reservation{ModelAttempts: 1, TokenUpperBound: int64(u.TotalTokens), CostUpperMicros: c}
}

func (m *metered) release(ctx context.Context, id string) {
	for _, key := range m.resources {
		m.permits.Release(context.WithoutCancel(ctx), key, id)
	}
}
func (m *metered) settle(ctx context.Context, ref runtimev1.ExecRef, id, status string, usage *control.Reservation) error {
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	return m.store.Settle(cleanup, ref, control.Settlement{AttemptID: id, Status: status, Usage: usage})
}

func (m *metered) Generate(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	return m.execute(ctx, input, opts, m.base.Generate)
}

func (m *metered) execute(ctx context.Context, input []*schema.Message, opts []em.Option, produce func(context.Context, []*schema.Message, ...em.Option) (*schema.Message, error)) (*schema.Message, error) {
	proof, ok := m.base.(*runtimeModel)
	call, bound := runtimeports.CurrentCall(ctx)
	if !ok || !bound || call.LogicalKey == "" || len(call.LogicalKey) > 150 || m.store == nil || m.permits == nil || len(m.resources) == 0 {
		return nil, model.ErrInvalidInput
	}
	key := "model:" + call.LogicalKey
	state, err := m.store.LoadCall(ctx, call.Ref, key, "model", "model:"+proof.config.ModelID)
	if err != nil {
		return nil, err
	}
	wire, normalized, err := m.options(proof, state.Execution.Limits, opts)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(map[string]any{"input": input, "options": normalized, "model": proof.fingerprint, "execution": state.Execution.Fingerprint})
	if err != nil {
		return nil, err
	}
	canonical, err := runtimeprotocol.CanonicalJSON(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	hash := hex.EncodeToString(sum[:])
	if state.Execution.Limits.CostUpperMicros > 0 && !proof.config.PriceKnown {
		return nil, model.ErrInvalidInput
	}
	if raw, exists, err := m.store.FindModelOutput(ctx, call.Ref, call.LogicalKey); err != nil {
		return nil, err
	} else if exists {
		var saved savedModelOutput
		if json.Unmarshal(raw, &saved) != nil || saved.SchemaVersion != 1 || saved.Message == nil || saved.Fingerprint != hash {
			return nil, runtimeports.ErrInputConflict
		}
		if err = m.settle(ctx, saved.Ref, saved.AttemptID, "succeeded", m.usage(proof, state.Execution, saved.Message)); err != nil {
			return nil, err
		}
		m.release(ctx, saved.AttemptID)
		return saved.Message, nil
	}
	for _, a := range state.Attempts {
		if a.InputHash != hash {
			return nil, runtimeports.ErrInputConflict
		}
	}
	if len(state.Attempts) > 0 {
		a := state.Attempts[len(state.Attempts)-1]
		if (a.Status == "reserved" || a.Status == "running" || a.Status == "unknown") && a.HoldUntil.After(time.Now()) {
			return nil, runtimeports.ErrInFlight
		}
		if a.NextRetryAt != nil {
			if err = waitModel(ctx, *a.NextRetryAt, state.Execution.Limits.DeadlineAt); err != nil {
				return nil, err
			}
		}
	}
	// This boundary performs one physical send. Durable retry policy is added
	// around it without reusing the legacy provider transport's retry loop.
	if len(state.Attempts) >= 4 {
		return nil, runtimeports.ErrRetryExhausted
	}
	maximum := int64(*em.GetCommonOptions(nil, wire...).MaxTokens)
	tokens := new(big.Int).Add(big.NewInt(proof.config.InputTokenUpperBound), big.NewInt(maximum))
	if !tokens.IsInt64() {
		return nil, model.ErrInvalidInput
	}
	price := int64(0)
	if state.Execution.Limits.CostUpperMicros > 0 {
		price, err = cost(proof.config.InputTokenUpperBound, maximum, proof.config.InputMicrosPerMillion, proof.config.OutputMicrosPerMillion)
		if err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(proof.config.RequestTimeout)
	if state.Execution.Limits.DeadlineAt.Before(deadline) {
		deadline = state.Execution.Limits.DeadlineAt
	}
	parentContext := ctx
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	id := runtimeports.AttemptID(call.Ref, key, len(state.Attempts)+1)
	hold := deadline.Add(proof.config.CleanupGrace)
	for _, resource := range m.resources {
		if err = m.permits.Acquire(ctx, resource, id, hold); err != nil {
			m.release(ctx, id)
			return nil, err
		}
	}
	attempt, err := m.store.Reserve(ctx, call.Ref, control.AttemptInput{Ref: call.Ref, AttemptID: id, LogicalKey: key, AttemptNo: len(state.Attempts) + 1, Capability: "model", Target: "model:" + proof.config.ModelID, ResourceKeys: m.resources, Reservation: control.Reservation{ModelAttempts: 1, TokenUpperBound: tokens.Int64(), CostUpperMicros: price}, HoldUntil: hold, InputHash: hash})
	if err != nil {
		m.release(ctx, id)
		return nil, err
	}
	if attempt.Replayed {
		return nil, runtimeports.ErrInFlight
	}
	message, err := produce(ctx, input, wire...)
	if err != nil || message == nil {
		var status *providerhttp.StatusError
		if errors.As(err, &status) {
			if settleErr := m.settle(ctx, call.Ref, id, "failed", nil); settleErr != nil {
				return nil, settleErr
			}
			m.release(ctx, id)
			if !errors.Is(err, model.ErrProviderTransient) {
				return nil, err
			}
			number := len(state.Attempts) + 1
			if number >= 4 {
				return nil, runtimeports.ErrRetryExhausted
			}
			delay := time.Duration(rand.Int64N(int64(time.Second<<uint(number-1)) + 1))
			if status.RetryDelay() > delay {
				delay = status.RetryDelay()
			}
			next := time.Now().Add(delay)
			if !next.Before(state.Execution.Limits.DeadlineAt) {
				return nil, context.DeadlineExceeded
			}
			// Scheduling and the next admission use the caller's original context;
			// this physical attempt's timeout must not shorten future attempts.
			if _, scheduleErr := m.store.ScheduleRetry(parentContext, call.Ref, id, next); scheduleErr != nil {
				return nil, scheduleErr
			}
			return m.execute(parentContext, input, opts, produce)
		}
		// A transport failure has no reliable token usage or completion boundary.
		settleErr := m.settle(ctx, call.Ref, id, "unknown", nil)
		if settleErr != nil {
			return nil, settleErr
		}
		if err == nil {
			err = model.ErrModelUnavailable
		}
		return nil, err
	}
	output, err := json.Marshal(savedModelOutput{SchemaVersion: 1, Fingerprint: hash, AttemptID: id, Ref: call.Ref, Message: message})
	if err == nil {
		err = m.store.SaveModelOutput(ctx, call.Ref, id, call.LogicalKey, output)
	}
	settleErr := m.settle(ctx, call.Ref, id, "succeeded", m.usage(proof, state.Execution, message))
	if settleErr == nil {
		m.release(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	if settleErr != nil {
		return nil, settleErr
	}
	return message, nil
}

func waitModel(ctx context.Context, at, deadline time.Time) error {
	if !at.Before(deadline) {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *metered) Stream(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	if _, ok := runtimeports.CurrentCall(ctx); !ok {
		return nil, model.ErrInvalidInput
	}
	reader, writer := schema.Pipe[*schema.Message](0)
	go func() {
		defer writer.Close()
		sent := false
		produce := func(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
			stream, err := m.base.Stream(ctx, input, opts...)
			if err != nil {
				return nil, err
			}
			defer stream.Close()
			chunks := []*schema.Message{}
			for {
				chunk, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return nil, err
				}
				if chunk == nil {
					continue
				}
				chunks = append(chunks, chunk)
				sent = true
				if writer.Send(chunk, nil) {
					return nil, context.Canceled
				}
			}
			return schema.ConcatMessages(chunks)
		}
		message, err := m.execute(ctx, input, opts, produce)
		if err != nil {
			writer.Send(nil, err)
			return
		}
		// A replay has no provider deltas; expose the already committed message.
		if !sent {
			writer.Send(message, nil)
		}
	}()
	return reader, nil
}
