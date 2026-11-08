package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// InputTokenUpperBound is an operator-verified provider hard limit. It must not
// be populated with a context-builder estimate. No API key enters fingerprints.
type RuntimeModelConfig struct {
	BaseURL, ModelID, APIKey                      string
	VerifiedInputBound                            bool
	InputTokenUpperBound, MaxOutputTokens         int64
	RequestTimeout, CleanupGrace                  time.Duration
	PriceKnown                                    bool
	InputMicrosPerMillion, OutputMicrosPerMillion int64
	SourceDefaults                                bool
}

type runtimeModel struct {
	em.ToolCallingChatModel
	config      RuntimeModelConfig
	fingerprint string
	tools       []*schema.ToolInfo
}

func NewRuntime(ctx context.Context, cfg RuntimeModelConfig) (em.ToolCallingChatModel, error) {
	if !cfg.VerifiedInputBound || cfg.InputTokenUpperBound <= 0 || cfg.MaxOutputTokens <= 0 || cfg.MaxOutputTokens > int64(^uint(0)>>1) || cfg.RequestTimeout <= 0 || cfg.CleanupGrace <= 0 || cfg.InputMicrosPerMillion < 0 || cfg.OutputMicrosPerMillion < 0 {
		return nil, model.ErrInvalidInput
	}
	client := providerhttp.SingleHTTPClient(&http.Client{Timeout: cfg.RequestTimeout})
	client.Transport = runtimeTransport{inner: client.Transport}
	base, err := newClient(ctx, cfg.BaseURL, cfg.ModelID, cfg.APIKey, client, cfg.SourceDefaults)
	if err != nil {
		return nil, err
	}
	safe := cfg
	safe.APIKey = ""
	raw, _ := json.Marshal(safe)
	sum := sha256.Sum256(raw)
	return &runtimeModel{ToolCallingChatModel: base, config: cfg, fingerprint: hex.EncodeToString(sum[:])}, nil
}

func (r *runtimeModel) WithTools(tools []*schema.ToolInfo) (em.ToolCallingChatModel, error) {
	raw, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	var frozen []*schema.ToolInfo
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	base, err := r.ToolCallingChatModel.WithTools(frozen)
	if err != nil {
		return nil, err
	}
	copy := *r
	copy.ToolCallingChatModel = base
	copy.tools = frozen
	return &copy, nil
}

func (r *runtimeModel) Generate(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.Message, error) {
	ctx, state := responseContext(ctx)
	message, err := r.ToolCallingChatModel.Generate(ctx, input, opts...)
	if message != nil {
		if message.ResponseMeta == nil {
			message.ResponseMeta = &schema.ResponseMeta{}
		}
		if state.usage.Load() == nil {
			message.ResponseMeta.Usage = nil
		}
	}
	return message, err
}

func (r *runtimeModel) Stream(ctx context.Context, input []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, state := responseContext(ctx)
	reader, err := r.ToolCallingChatModel.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	// Preserve the wire's usage absence. SDKs sometimes synthesize an all-zero
	// usage struct for a provider response that omitted accounting entirely.
	return schema.StreamReaderWithConvert(reader, func(message *schema.Message) (*schema.Message, error) {
		if message != nil && message.ResponseMeta != nil && state.usage.Load() == nil {
			message.ResponseMeta.Usage = nil
		}
		return message, nil
	}), nil
}
