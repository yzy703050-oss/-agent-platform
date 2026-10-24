package travel

import (
	"context"
	"encoding/json"
	"time"

	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type PolicyAgent struct {
	model     em.BaseChatModel
	retriever tripsvc.PolicyRetriever
}

func NewPolicyAgent(model em.BaseChatModel, retriever tripsvc.PolicyRetriever) *PolicyAgent {
	return &PolicyAgent{model: model, retriever: retriever}
}
func (a *PolicyAgent) Query(ctx context.Context, _ pm.TravelExecution, request pm.TravelPolicyQuery) (pm.SourcePolicyContext, error) {
	empty := pm.SourcePolicyContext{Status: "no_knowledge", Summary: "未取得适用政策资料，不能确认无限制。", Constraints: []pm.SourcePolicyRule{}, Applicability: map[string]json.RawMessage{}, References: []pm.SourcePolicyReference{}, MissingFields: []string{"policy_evidence"}, Conflicts: []json.RawMessage{}}
	if a.retriever == nil {
		return empty, nil
	}
	var query string
	_ = json.Unmarshal(request.Context["original_query"], &query)
	retrievalTimeout := request.RetrievalTimeout
	if retrievalTimeout <= 0 {
		retrievalTimeout = 30 * time.Second
	}
	retrievalCtx, retrievalCancel := context.WithTimeout(ctx, retrievalTimeout)
	evidence, err := a.retriever.Search(retrievalCtx, query)
	retrievalCancel()
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, nil
	}
	refs := []pm.SourcePolicyReference{}
	fragments := []any{}
	seen := map[string]bool{}
	for _, f := range evidence.Fragments {
		if f.ID == "" || f.Content == "" || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		refs = append(refs, pm.SourcePolicyReference{ID: f.ID, Source: f.Source, Location: f.Location, DocumentID: f.Metadata["document_id"], ChunkID: f.Metadata["chunk_id"], Version: f.Metadata["version"], EffectiveDate: f.Metadata["effective_date"], Title: f.Metadata["title"]})
		metadata := f.Metadata
		if metadata == nil {
			metadata = map[string]json.RawMessage{}
		}
		fragments = append(fragments, map[string]any{"id": f.ID, "content": f.Content, "metadata": metadata})
	}
	if len(refs) == 0 {
		return empty, nil
	}
	payload := struct {
		References []pm.SourcePolicyReference `json:"references"`
		Fragments  []any                      `json:"fragments"`
	}{refs, fragments}
	rawEvidence, _ := json.Marshal(payload)
	rawContext, _ := json.Marshal(request.Context)
	prompt, err := buildPolicyPrompt(rawContext, rawEvidence)
	if err != nil {
		return empty, err
	}
	response, err := a.model.Generate(withModelStage(ctx, "agent:travel_policy"), []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, &pm.TravelRuleError{Code: "POLICY_EXTRACTION_FAILED", Message: "未取得有效政策提取结果，资料仍有缺口。"}
	}
	if sourceProtocolFailure(response) != "" || len(response.ToolCalls) > 0 {
		return empty, pm.ErrInvalidInput
	}
	var raw json.RawMessage
	if err = parseAuxiliaryJSON(response.Content, &raw); err != nil {
		return empty, &pm.TravelRuleError{Code: "POLICY_EXTRACTION_FAILED", Message: "未取得有效政策提取结果，资料仍有缺口。"}
	}
	policy, err := tripsvc.ValidateSourcePolicy(raw, request.Context, refs)
	if err != nil {
		return empty, err
	}
	if evidence.IsDemo {
		policy.Status = "partial"
		policy.Summary = "演示政策：" + policy.Summary
		policy.MissingFields = append(policy.MissingFields, "demo_policy_not_authoritative")
	}
	return policy, nil
}
