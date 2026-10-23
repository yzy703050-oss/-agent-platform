package llm

import (
	"agent-platform/backend/internal/model"
	"context"
	"github.com/cloudwego/eino-ext/components/model/openai"
	em "github.com/cloudwego/eino/components/model"
	"net/url"
	"time"
)

func New(ctx context.Context, baseURL, modelID, key string) (em.ToolCallingChatModel, error) {
	u, e := url.Parse(baseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || modelID == "" || key == "" {
		return nil, model.ErrModelUnavailable
	}
	client, e := openai.NewChatModel(ctx, &openai.ChatModelConfig{BaseURL: baseURL, APIKey: key, Model: modelID, Timeout: 120 * time.Second})
	if e != nil {
		return nil, model.ErrModelUnavailable
	}
	return client, nil
}
