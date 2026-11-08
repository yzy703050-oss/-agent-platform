package toolquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/control"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/permit"
	"agent-platform/backend/internal/runtimeports"
)

type Service struct {
	store     Store
	permits   permit.Service
	providers map[string]Provider
	options   Options
}

func providerKey(domain, mode, name string) string { return domain + "\x00" + mode + "\x00" + name }

func New(store Store, permits permit.Service, providers []Provider, options Options) (*Service, error) {
	if store == nil || permits == nil || options.CleanupGrace <= 0 {
		return nil, model.ErrInvalidInput
	}
	if options.Jitter == nil {
		options.Jitter = defaultJitter
	}
	s := &Service{store: store, permits: permits, providers: map[string]Provider{}, options: options}
	for _, p := range providers {
		if strings.TrimSpace(p.Domain) == "" || p.Name == "" || (p.Mode != "live" && p.Mode != "demo") || len(p.ResourceKeys) == 0 || p.Validate == nil || p.Call == nil {
			return nil, model.ErrInvalidInput
		}
		key := providerKey(p.Domain, p.Mode, p.Name)
		if _, exists := s.providers[key]; exists {
			return nil, model.ErrConflict
		}
		p.ResourceKeys = append([]string(nil), p.ResourceKeys...)
		s.providers[key] = p
	}
	return s, nil
}

func (s *Service) FindSaved(ctx context.Context, ref runtimev1.ExecRef, key string) (runtimev1.SharedToolEvidenceRecord, bool, error) {
	return s.store.FindSaved(ctx, ref, key)
}

func (s *Service) Execute(ctx context.Context, ref runtimev1.ExecRef, in QueryInput) (runtimev1.SharedToolEvidenceRecord, error) {
	var result runtimev1.SharedToolEvidenceRecord
	if in.CallKey == "" || len(in.CallKey) > 160 {
		return result, model.ErrInvalidInput
	}
	provider, ok := s.providers[providerKey(in.Domain, in.Mode, in.Provider)]
	if !ok {
		return result, ErrUnavailable
	}
	if err := provider.Validate(in.Arguments); err != nil {
		return result, err
	}
	state, err := s.store.BeginQuery(ctx, ref, in)
	if err != nil {
		return result, err
	}
	if saved, exists, err := s.store.FindSaved(ctx, ref, in.CallKey); err != nil {
		return result, err
	} else if exists {
		return saved, nil
	}
	if state.Record.Status == "failed" {
		return result, fmt.Errorf("original logical query failed")
	}
	ctx, cancel := context.WithDeadline(ctx, state.DeadlineAt)
	defer cancel()
	key := "tool:" + in.CallKey
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		call, err := s.store.LoadCall(ctx, ref, key, "tool", in.Domain)
		if err != nil {
			return result, err
		}
		if len(call.Attempts) >= 4 {
			return result, s.fail(ctx, ref, state.Record.ID, "retry_exhausted", runtimeports.ErrRetryExhausted)
		}
		if len(call.Attempts) > 0 {
			last := call.Attempts[len(call.Attempts)-1]
			if last.InputHash != state.Record.InputHash {
				return result, runtimeports.ErrInputConflict
			}
			if (last.Status == "reserved" || last.Status == "running" || last.Status == "unknown") && last.HoldUntil.After(time.Now()) {
				return result, runtimeports.ErrInFlight
			}
			if last.NextRetryAt != nil {
				if err = waitUntil(ctx, *last.NextRetryAt); err != nil {
					return result, err
				}
			}
		}
		number := len(call.Attempts) + 1
		attemptID := runtimeports.AttemptID(ref, key, number)
		holdUntil := state.DeadlineAt.Add(s.options.CleanupGrace)
		acquired := []string{}
		for _, resource := range provider.ResourceKeys {
			if err = s.permits.Acquire(ctx, resource, attemptID, holdUntil); err != nil {
				for _, held := range acquired {
					s.permits.Release(context.WithoutCancel(ctx), held, attemptID)
				}
				return result, err
			}
			acquired = append(acquired, resource)
		}
		a, err := s.store.Reserve(ctx, ref, control.AttemptInput{Ref: ref, AttemptID: attemptID, LogicalKey: key, AttemptNo: number, Capability: "tool", Target: in.Domain, ResourceKeys: provider.ResourceKeys, Reservation: control.Reservation{ToolAttempts: 1}, HoldUntil: holdUntil, InputHash: state.Record.InputHash})
		if err != nil {
			for _, resource := range acquired {
				s.permits.Release(context.WithoutCancel(ctx), resource, attemptID)
			}
			return result, err
		}
		if a.Replayed {
			return result, runtimeports.ErrInFlight
		}
		callCtx := runtimeports.WithExecution(runtimeports.WithCall(ctx, ref, key, number), call.Execution)
		response, callErr := provider.Call(callCtx, in.Arguments)
		if callErr == nil {
			result, err = s.store.SaveQuery(ctx, ref, state.Record.ID, attemptID, response)
			if err == nil {
				for _, resource := range acquired {
					s.permits.Release(context.WithoutCancel(ctx), resource, attemptID)
				}
				return result, nil
			}
			// The response was complete, but its transaction did not commit. Keep
			// the used attempt; a later read-only replay may recompute within limits.
			settleCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			s.store.Settle(settleCtx, ref, control.Settlement{AttemptID: attemptID, Status: "succeeded", Usage: &control.Reservation{ToolAttempts: 1}})
			stop()
			for _, resource := range acquired {
				s.permits.Release(context.WithoutCancel(ctx), resource, attemptID)
			}
			return result, err
		}
		status := "failed"
		usage := &control.Reservation{ToolAttempts: 1}
		if ctx.Err() != nil || errors.Is(callErr, providerhttp.ErrRequest) {
			status = "unknown"
			usage = nil
		}
		settleCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		settleErr := s.store.Settle(settleCtx, ref, control.Settlement{AttemptID: attemptID, Status: status, Usage: usage})
		stop()
		if status != "unknown" {
			for _, resource := range acquired {
				s.permits.Release(context.WithoutCancel(ctx), resource, attemptID)
			}
		}
		if settleErr != nil {
			return result, settleErr
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if status == "unknown" {
			return result, runtimeports.ErrInFlight
		}
		if in.Domain == "web" || !retryable(callErr) {
			return result, s.fail(ctx, ref, state.Record.ID, "provider_failed", callErr)
		}
		if number == 4 {
			return result, s.fail(ctx, ref, state.Record.ID, "retry_exhausted", runtimeports.ErrRetryExhausted)
		}
		delay := s.options.Jitter(time.Second << uint(number-1))
		var after retryAfter
		if errors.As(callErr, &after) && after.RetryDelay() > delay {
			delay = after.RetryDelay()
		}
		next := time.Now().Add(delay)
		if !next.Before(state.DeadlineAt) {
			return result, s.fail(ctx, ref, state.Record.ID, "retry_deadline", context.DeadlineExceeded)
		}
		next, err = s.store.ScheduleRetry(ctx, ref, attemptID, next)
		if err != nil {
			return result, err
		}
		if err = waitUntil(ctx, next); err != nil {
			return result, err
		}
	}
}

func (s *Service) fail(ctx context.Context, ref runtimev1.ExecRef, id, code string, cause error) error {
	category := "unsupported"
	if errors.Is(cause, model.ErrProviderTransient) || errors.Is(cause, runtimeports.ErrRetryExhausted) {
		category = "transient"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		category = "deadline"
	}
	problem := &runtimev1.ErrorDetail{Code: code, Category: category, Message: "查询未能完成", RetryExhausted: code == "retry_exhausted", ModelAction: "report_partial"}
	if err := s.store.FailQuery(ctx, ref, id, problem); err != nil {
		return err
	}
	return cause
}

// TravelProvider adapts the existing client contract without introducing plans
// or inventing provider fields. Original results stay in the immutable snapshot.
func TravelProvider(domain, name, mode string, resources []string, search func(context.Context, model.TravelQuery) (model.TravelEvidence, error)) Provider {
	decode := func(raw json.RawMessage) (model.TravelQuery, error) {
		var q model.TravelQuery
		if err := ValidateObject(raw); err != nil {
			return q, err
		}
		// Executor's formal Task parameters use departure_date and keyword
		// arrays. Translate those representations into the retained client DTO;
		// query_context still records the original accepted arguments.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return q, err
		}
		if departure, exists := fields["departure_date"]; exists {
			if domain != "train" {
				return q, model.ErrInvalidInput
			}
			if _, duplicate := fields["date"]; duplicate {
				return q, model.ErrInvalidInput
			}
			fields["date"] = departure
			delete(fields, "departure_date")
		}
		if keywords, exists := fields["keywords"]; exists && strings.HasPrefix(strings.TrimSpace(string(keywords)), "[") {
			var values []string
			if err := json.Unmarshal(keywords, &values); err != nil || values == nil {
				return q, model.ErrInvalidInput
			}
			encoded, _ := json.Marshal(strings.Join(values, " "))
			fields["keywords"] = encoded
		}
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return q, err
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			return q, model.ErrInvalidInput
		}
		if q.PlanID != "" || q.TaskID != "" || q.PlanRevision != 0 {
			return q, model.ErrInvalidInput
		}
		if q.Domain != "" && q.Domain != domain {
			return q, model.ErrInvalidInput
		}
		q.Domain = domain
		if (domain == "hotel" || domain == "weather") && strings.TrimSpace(q.City) == "" {
			return q, model.ErrInvalidInput
		}
		if domain == "train" && (q.Origin == "" || q.Destination == "" || q.Date == "") {
			return q, model.ErrInvalidInput
		}
		return q, nil
	}
	return Provider{Domain: domain, Name: name, Mode: mode, ResourceKeys: resources, Validate: func(raw json.RawMessage) error { _, err := decode(raw); return err }, Call: func(ctx context.Context, raw json.RawMessage) (Result, error) {
		q, err := decode(raw)
		if err != nil {
			return Result{}, err
		}
		data, err := search(ctx, q)
		if err != nil {
			return Result{}, err
		}
		if data.Status == "unavailable" || data.Status == "failed" {
			return Result{}, ErrUnavailable
		}
		if data.IsDemo != (mode == "demo") {
			return Result{}, model.ErrConflict
		}
		candidates := []runtimev1.Candidate{}
		for _, row := range data.Candidates {
			if row.IsDemo != data.IsDemo {
				return Result{}, model.ErrConflict
			}
			attrs := map[string]json.RawMessage{}
			for key, value := range map[string]any{"origin": row.Origin, "destination": row.Destination, "departure_at": row.DepartureAt, "arrival_at": row.ArrivalAt, "city": row.City, "address": row.Address, "availability": row.Availability, "remaining": row.Remaining, "is_demo": row.IsDemo, "train_kind": row.TrainKind, "seat_class": row.SeatClass} {
				b, _ := json.Marshal(value)
				attrs[key] = b
			}
			candidate := runtimev1.Candidate{CandidateID: row.ID, Title: row.Name, Attributes: attrs, MissingFields: []string{}}
			if row.PriceFen != nil {
				if *row.PriceFen < 0 {
					return Result{}, model.ErrInvalidInput
				}
				basis := "unknown"
				if domain == "train" {
					basis = "person"
				}
				candidate.Price = &runtimev1.Money{AmountMinor: *row.PriceFen, Currency: "CNY", Basis: basis}
			}
			if row.URL != "" {
				url := row.URL
				candidate.SourceURL = &url
			}
			if domain == "hotel" && row.PriceFen == nil {
				candidate.MissingFields = append(candidate.MissingFields, "price", "inventory")
			}
			candidates = append(candidates, candidate)
		}
		snapshot := map[string]json.RawMessage{}
		for key, value := range map[string]any{"result": data, "candidates": candidates, "source": data.Source, "actual_query": data.Query, "is_demo": data.IsDemo} {
			b, err := json.Marshal(value)
			if err != nil {
				return Result{}, err
			}
			snapshot[key] = b
		}
		coverage := "complete"
		if data.Status == "partial" {
			coverage = "partial"
		}
		if len(candidates) == 0 && len(data.Weather) == 0 {
			coverage = "empty"
		}
		fetched := data.Source.FetchedAt
		if fetched.IsZero() {
			fetched = time.Now().UTC()
		}
		var expires *time.Time
		if !data.ExpiresAt.IsZero() {
			expires = &data.ExpiresAt
		}
		return Result{Snapshot: snapshot, Coverage: coverage, FetchedAt: fetched, ExpiresAt: expires}, nil
	}}
}
