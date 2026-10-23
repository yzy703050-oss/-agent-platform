// Package mcp adapts the existing RAG server to business policy evidence.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agent-platform/backend/internal/model"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrProtocol = errors.New("invalid policy evidence response")

type Caller interface {
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
}
type PolicyClient struct {
	caller     Caller
	collection string
}

func NewPolicyClient(c Caller, collection string) (*PolicyClient, error) {
	if c == nil || strings.TrimSpace(collection) == "" || strings.ContainsAny(collection, "/\\") || strings.Contains(collection, "..") {
		return nil, model.ErrInvalidInput
	}
	return &PolicyClient{caller: c, collection: collection}, nil
}
func (p *PolicyClient) Search(ctx context.Context, q string) (model.PolicyEvidence, error) {
	r, err := p.caller.CallTool(ctx, &sdk.CallToolParams{Name: "query_policy_evidence", Arguments: map[string]any{"query": q, "collection": p.collection, "top_k": 5}})
	if err != nil {
		return model.PolicyEvidence{}, errors.New("policy retrieval unavailable")
	}
	if r == nil || r.IsError {
		return model.PolicyEvidence{}, errors.New("policy retrieval failed")
	}
	var data []byte
	if r.StructuredContent != nil {
		data, err = json.Marshal(r.StructuredContent)
	} else {
		for _, c := range r.Content {
			if text, ok := c.(*sdk.TextContent); ok {
				if data != nil {
					return model.PolicyEvidence{}, ErrProtocol
				}
				data = []byte(text.Text)
			}
		}
	}
	if err != nil || len(data) == 0 || len(data) > 1000000 {
		return model.PolicyEvidence{}, ErrProtocol
	}
	return decodePolicy(data, p.collection)
}
func decodePolicy(data []byte, collection string) (model.PolicyEvidence, error) {
	var wire struct {
		Version    int    `json:"schema_version"`
		Status     string `json:"status"`
		Collection string `json:"collection"`
		Fragments  []struct {
			ID       string   `json:"id"`
			ChunkID  string   `json:"chunk_id"`
			Content  string   `json:"content"`
			Score    *float64 `json:"score"`
			Metadata struct {
				Source        string          `json:"source"`
				Location      json.RawMessage `json:"location"`
				Region        string          `json:"region"`
				EmployeeLevel string          `json:"employee_level"`
				EffectiveFrom string          `json:"effective_from"`
				EffectiveTo   string          `json:"effective_to"`
			} `json:"metadata"`
		} `json:"fragments"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Version != 1 || wire.Collection != collection || (wire.Status != "ok" && wire.Status != "no_knowledge") || len(wire.Fragments) > 20 {
		return model.PolicyEvidence{}, ErrProtocol
	}
	if (wire.Status == "ok") != (len(wire.Fragments) > 0) {
		return model.PolicyEvidence{}, ErrProtocol
	}
	out := model.PolicyEvidence{Status: wire.Status, Collection: wire.Collection, IsDemo: collection == "agent_platform_demo"}
	seen := map[string]bool{}
	for _, f := range wire.Fragments {
		if f.ID == "" || f.ChunkID != f.ID || strings.TrimSpace(f.Content) == "" || f.Metadata.Source == "" || seen[f.ID] {
			return model.PolicyEvidence{}, ErrProtocol
		}
		seen[f.ID] = true
		out.Fragments = append(out.Fragments, model.PolicyFragment{ID: f.ID, Content: f.Content, Source: f.Metadata.Source, Location: f.Metadata.Location, Score: f.Score, Conditions: model.PolicyConditions{Region: f.Metadata.Region, EmployeeLevel: f.Metadata.EmployeeLevel, EffectiveFrom: f.Metadata.EffectiveFrom, EffectiveTo: f.Metadata.EffectiveTo}})
	}
	return out, nil
}
