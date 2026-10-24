package llm

import (
	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
	"context"
	"github.com/cloudwego/eino-ext/components/model/openai"
	em "github.com/cloudwego/eino/components/model"
	"net/http"
	"net/url"
	"time"
)

func New(ctx context.Context, baseURL, modelID, key string) (em.ToolCallingChatModel, error) {
	return newClient(ctx, baseURL, modelID, key, nil)
}

// Travel's original network policy is local to its complete-response client;
// General keeps the existing streaming transport and request defaults.
func NewTravel(ctx context.Context, baseURL, modelID, key string) (em.ToolCallingChatModel, error) {
	client := &http.Client{Timeout: 120 * time.Second, Transport: providerhttp.NewResilientTransport(nil, providerhttp.ResilienceOptions{RequestTimeout: 30 * time.Second}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return newClient(ctx, baseURL, modelID, key, client)
}
func newClient(ctx context.Context, baseURL, modelID, key string, httpClient *http.Client) (em.ToolCallingChatModel, error) {
	u, e := url.Parse(baseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || modelID == "" || key == "" {
		return nil, model.ErrModelUnavailable
	}
	client, e := openai.NewChatModel(ctx, &openai.ChatModelConfig{BaseURL: baseURL, APIKey: key, Model: modelID, Timeout: 120 * time.Second, HTTPClient: httpClient})
	if e != nil {
		return nil, model.ErrModelUnavailable
	}
	return client, nil
}
