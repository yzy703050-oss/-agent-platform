package llm

import (
	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
	"context"
	"github.com/cloudwego/eino-ext/components/model/openai"
	em "github.com/cloudwego/eino/components/model"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func New(ctx context.Context, baseURL, modelID, key string) (em.ToolCallingChatModel, error) {
	return newClient(ctx, baseURL, modelID, key, nil, false)
}

// Travel's original network policy is local to its complete-response client;
// General keeps the existing streaming transport and request defaults.
func NewTravel(ctx context.Context, baseURL, modelID, key string) (em.ToolCallingChatModel, error) {
	client := &http.Client{Timeout: 120 * time.Second, Transport: providerhttp.NewResilientTransport(nil, providerhttp.ResilienceOptions{RequestTimeout: 30 * time.Second}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return newClient(ctx, baseURL, modelID, key, client, true)
}
func newClient(ctx context.Context, baseURL, modelID, key string, httpClient *http.Client, sourceDefaults bool) (em.ToolCallingChatModel, error) {
	u, e := url.Parse(baseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || modelID == "" || key == "" {
		return nil, model.ErrModelUnavailable
	}
	config := &openai.ChatModelConfig{BaseURL: baseURL, APIKey: key, Model: modelID, Timeout: 120 * time.Second, HTTPClient: httpClient}
	if sourceDefaults {
		temperature := float32(0.3)
		maxTokens := 8192
		config.Temperature = &temperature
		config.MaxTokens = &maxTokens
		if strings.HasPrefix(modelID, "deepseek") {
			config.ExtraFields = map[string]any{"thinking": map[string]any{"type": "disabled"}}
		}
	}
	client, e := openai.NewChatModel(ctx, config)
	if e != nil {
		return nil, model.ErrModelUnavailable
	}
	return client, nil
}
