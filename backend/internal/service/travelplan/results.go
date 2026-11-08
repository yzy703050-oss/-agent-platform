package travelplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
)

func StaleResult(request v1.ExecutionRequest) v1.ExecutionResult {
	return v1.ExecutionResult{SchemaVersion: 1, ExecutionStepID: request.ExecutionStepID, PlanID: request.PlanID, TaskID: request.TaskID, TaskRevision: request.TaskRevision, ItemID: request.ItemID, Generation: request.Generation, Status: "stale", Coverage: "unavailable", Candidates: []v1.Candidate{}, MissingFields: []string{}, Error: &v1.ErrorDetail{Code: "stale_execution", Category: "conflict", Message: "当前任务版本或查询代次已变化", ModelAction: "replan"}}
}

// EvidenceResult preserves provider facts, including unknown money. It checks
// representation and provenance metadata without treating preferences as filters.
func EvidenceResult(request v1.ExecutionRequest, view evidence.View) (v1.ExecutionResult, error) {
	result := v1.ExecutionResult{SchemaVersion: 1, ExecutionStepID: request.ExecutionStepID, PlanID: request.PlanID, TaskID: request.TaskID, TaskRevision: request.TaskRevision, ItemID: request.ItemID, Generation: request.Generation, Status: "succeeded", Candidates: []v1.Candidate{}, MissingFields: []string{}}
	var metadata evidence.QueryContext
	if json.Unmarshal(view.Record.Snapshot["query_context"], &metadata) != nil || metadata.Domain != view.Record.Domain || metadata.Mode != view.Record.Mode || metadata.Provider != view.Record.Provider || metadata.InputHash == "" {
		return result, model.ErrConflict
	}
	if metadata.Coverage != "complete" && metadata.Coverage != "partial" && metadata.Coverage != "empty" {
		return result, model.ErrConflict
	}
	result.Coverage = metadata.Coverage
	if raw, exists := view.Record.Snapshot["candidates"]; exists {
		if err := json.Unmarshal(raw, &result.Candidates); err != nil || result.Candidates == nil {
			return result, model.ErrInvalidInput
		}
	}
	if request.Domain == "weather" && len(result.Candidates) == 0 {
		var original struct {
			Weather []map[string]json.RawMessage `json:"weather"`
			Source  struct {
				URL string `json:"url"`
			} `json:"source"`
		}
		if raw, exists := view.Record.Snapshot["result"]; exists {
			if err := json.Unmarshal(raw, &original); err != nil {
				return result, err
			}
			for _, point := range original.Weather {
				canonicalPoint, err := canonical(point)
				if err != nil {
					return result, err
				}
				hash := sha256.Sum256([]byte(canonicalPoint))
				title := "天气资料"
				if text, exists := point["description"]; exists {
					if json.Unmarshal(text, &title) != nil || title == "" {
						title = "天气资料"
					}
				}
				candidate := v1.Candidate{CandidateID: "weather:" + hex.EncodeToString(hash[:]), Title: title, Attributes: point, MissingFields: []string{}}
				if original.Source.URL != "" {
					link := original.Source.URL
					candidate.SourceURL = &link
				}
				if value, exists := point["temperature_c"]; !exists || string(value) == "null" {
					candidate.MissingFields = append(candidate.MissingFields, "temperature_c")
				}
				result.Candidates = append(result.Candidates, candidate)
			}
		}
	}
	ids := map[string]bool{}
	for _, candidate := range result.Candidates {
		var validated v1.Candidate
		if err := checked(candidate, "Candidate", &validated); err != nil {
			return result, err
		}
		if ids[candidate.CandidateID] {
			return result, model.ErrInvalidInput
		}
		ids[candidate.CandidateID] = true
		for _, missing := range candidate.MissingFields {
			if !slices.Contains(result.MissingFields, missing) {
				result.MissingFields = append(result.MissingFields, missing)
			}
		}
	}
	if result.Coverage == "empty" && len(result.Candidates) > 0 {
		return result, model.ErrInvalidInput
	}
	if len(result.MissingFields) > 0 && result.Coverage == "complete" {
		result.Coverage = "partial"
	}
	if view.Record.Domain != request.Domain || view.ReferenceID == "" || view.ResultRevision != view.Record.ResultRevision {
		return result, model.ErrConflict
	}
	id, revision, retrieved := view.ReferenceID, view.ResultRevision, view.Record.FetchedAt
	result.EvidenceID = &id
	result.ResultRevision = &revision
	result.RetrievedAt = &retrieved
	result.ValidUntil = view.Record.ExpiresAt
	var validated v1.ExecutionResult
	return result, checked(result, "ExecutionResult", &validated)
}
