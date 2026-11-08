package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type PolicyQuery struct {
	Query       string          `json:"query"`
	Collection  string          `json:"collection"`
	QueryType   string          `json:"query_type,omitempty"`
	TopK        int             `json:"top_k"`
	TimeContext json.RawMessage `json:"time_context,omitempty"`
}
type EvidenceResponse struct {
	Evidence       model.PolicyEvidence
	Execution, Raw json.RawMessage
}

func (c *HTTPClient) CallEvidence(ctx context.Context, key string, query PolicyQuery) (EvidenceResponse, error) {
	var out EvidenceResponse
	call, ok := runtimeports.CurrentCall(ctx)
	if !ok || call.Execution == nil || key == "" || call.LogicalKey != key || call.AttemptNo < 1 || call.AttemptNo > 4 {
		return out, model.ErrInvalidInput
	}
	e := call.Execution
	scope := e.Scope
	ref := call.Ref
	if scope.ScopeID != ref.ScopeID || scope.ScopeHash != ref.ScopeHash || scope.OwnerService != c.cfg.ServiceID || e.Service != c.cfg.ServiceID || ref.OwnerID == "" || ref.ThreadID == "" || ref.Lease.Epoch < 1 || ref.Lease.WorkerID == "" || !ref.Lease.ExpiresAt.After(time.Now()) || scope.SubjectID != e.UserID || scope.ConversationID != e.ConversationID || scope.SourceRunID != e.RunID || scope.ContextEpoch != e.ContextEpoch {
		return out, model.ErrInvalidInput
	}
	allowed := false
	for _, collection := range c.cfg.Collections {
		if collection == query.Collection {
			allowed = true
		}
	}
	hasCap, hasTarget := false, false
	for _, v := range scope.Capabilities {
		if v == "tool" {
			hasCap = true
		}
	}
	for _, v := range scope.Targets {
		if v == "policy" {
			hasTarget = true
		}
	}
	if !allowed || !hasCap || !hasTarget || strings.TrimSpace(query.Query) == "" || query.TopK < 1 || query.TopK > 20 {
		return out, model.ErrInvalidInput
	}
	args, err := json.Marshal(query)
	if err != nil {
		return out, err
	}
	if _, err = runtimeprotocol.CanonicalJSON(args); err != nil {
		return out, err
	}
	deadline := e.Limits.DeadlineAt
	if scope.ExpiresAt.Before(deadline) {
		deadline = scope.ExpiresAt
	}
	if !deadline.After(time.Now()) {
		return out, context.DeadlineExceeded
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	session, err := c.connect(ctx)
	if err != nil {
		return out, err
	}
	meta := runtimev1.TrustedTravelMeta{SchemaVersion: 1, ScopeID: ref.ScopeID, ScopeHash: ref.ScopeHash, SubjectID: e.UserID, ConversationID: e.ConversationID, SourceRunID: e.RunID, DelegationID: scope.DelegationID, SourceContextEpoch: e.ContextEpoch, DeadlineAt: deadline.UTC().Format(time.RFC3339Nano)}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "query_policy_evidence", Arguments: json.RawMessage(args), Meta: sdk.Meta{"xiaodoubao/runtime": meta}})
	if err != nil {
		return out, err
	}
	if result == nil || result.IsError {
		return out, ErrProtocol
	}
	if result.StructuredContent != nil {
		out.Raw, err = json.Marshal(result.StructuredContent)
	} else {
		for _, content := range result.Content {
			if text, ok := content.(*sdk.TextContent); ok {
				if out.Raw != nil {
					return out, ErrProtocol
				}
				out.Raw = json.RawMessage(text.Text)
			}
		}
	}
	if err != nil || len(out.Raw) == 0 {
		return out, ErrProtocol
	}
	if _, err = runtimeprotocol.CanonicalJSON(out.Raw); err != nil {
		return out, ErrProtocol
	}
	out.Evidence, err = decodePolicy(out.Raw, query.Collection)
	if err != nil {
		return EvidenceResponse{}, err
	}
	var extra map[string]json.RawMessage
	if json.Unmarshal(out.Raw, &extra) != nil {
		return EvidenceResponse{}, ErrProtocol
	}
	out.Execution = extra["execution"]
	return out, nil
}
