package toolquery

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"agent-platform/backend/internal/client/mcp"
	"agent-platform/backend/internal/client/websearch"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
)

func strictArguments(raw json.RawMessage, out any) error {
	if err := ValidateObject(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return model.ErrInvalidInput
	}
	return nil
}

func WebProvider(name string, resources []string, search func(context.Context, websearch.Request) (websearch.Response, error)) Provider {
	decode := func(raw json.RawMessage) (websearch.Request, error) {
		var request websearch.Request
		if err := strictArguments(raw, &request); err != nil {
			return request, err
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		for _, v := range fields {
			if string(v) == "null" {
				return request, model.ErrInvalidInput
			}
		}
		request.Query = strings.TrimSpace(request.Query)
		if len([]rune(request.Query)) < 1 || len([]rune(request.Query)) > 500 {
			return request, model.ErrInvalidInput
		}
		switch request.TimeRange {
		case "", "d", "w", "m", "y":
			return request, nil
		default:
			return request, model.ErrInvalidInput
		}
	}
	return Provider{Domain: "web", Name: name, Mode: "live", ResourceKeys: resources, Validate: func(raw json.RawMessage) error { _, err := decode(raw); return err }, Call: func(ctx context.Context, raw json.RawMessage) (Result, error) {
		request, err := decode(raw)
		if err != nil {
			return Result{}, err
		}
		response, err := search(ctx, request)
		if err != nil {
			return Result{}, err
		}
		response = websearch.Normalize(response)
		if response.Status != "ok" && response.Status != "no_results" {
			return Result{}, ErrUnavailable
		}
		candidates := []runtimev1.Candidate{}
		for _, row := range response.Results {
			link := row.URL
			summary, _ := json.Marshal(row.Summary)
			candidates = append(candidates, runtimev1.Candidate{CandidateID: row.URL, Title: row.Title, Price: nil, Attributes: map[string]json.RawMessage{"summary": summary}, SourceURL: &link, MissingFields: []string{}})
		}
		snapshot := map[string]json.RawMessage{}
		for k, v := range map[string]any{"response": response, "candidates": candidates, "actual_query": request} {
			b, err := json.Marshal(v)
			if err != nil {
				return Result{}, err
			}
			snapshot[k] = b
		}
		coverage := "complete"
		if response.Status == "no_results" {
			coverage = "empty"
		}
		return Result{Snapshot: snapshot, Coverage: coverage, FetchedAt: time.Now().UTC()}, nil
	}}
}

func PolicyProvider(name string, resources []string, query func(context.Context, string, mcp.PolicyQuery) (mcp.EvidenceResponse, error)) Provider {
	decode := func(raw json.RawMessage) (mcp.PolicyQuery, error) {
		input := mcp.PolicyQuery{TopK: 5}
		if err := strictArguments(raw, &input); err != nil {
			return input, err
		}
		if strings.TrimSpace(input.Query) == "" || input.Collection == "" || strings.ContainsAny(input.Collection, "/\\") || strings.Contains(input.Collection, "..") || input.TopK < 1 || input.TopK > 20 {
			return input, model.ErrInvalidInput
		}
		if len(input.TimeContext) > 0 {
			if err := ValidateObject(input.TimeContext); err != nil {
				return input, err
			}
		}
		return input, nil
	}
	return Provider{Domain: "policy", Name: name, Mode: "live", ResourceKeys: resources, Validate: func(raw json.RawMessage) error { _, err := decode(raw); return err }, Call: func(ctx context.Context, raw json.RawMessage) (Result, error) {
		input, err := decode(raw)
		if err != nil {
			return Result{}, err
		}
		call, ok := runtimeports.CurrentCall(ctx)
		if !ok || call.LogicalKey == "" {
			return Result{}, model.ErrInvalidInput
		}
		response, err := query(ctx, call.LogicalKey, input)
		if err != nil {
			return Result{}, err
		}
		if response.Evidence.Status != "ok" && response.Evidence.Status != "no_knowledge" {
			return Result{}, ErrUnavailable
		}
		snapshot := map[string]json.RawMessage{"raw": response.Raw}
		if len(response.Execution) > 0 {
			snapshot["execution"] = response.Execution
		}
		body, err := json.Marshal(response.Evidence)
		if err != nil {
			return Result{}, err
		}
		snapshot["evidence"] = body
		coverage := "complete"
		if response.Evidence.Status == "no_knowledge" {
			coverage = "empty"
		}
		return Result{Snapshot: snapshot, Coverage: coverage, FetchedAt: time.Now().UTC()}, nil
	}}
}
