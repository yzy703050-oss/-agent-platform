// Package websearch adapts the official Eino DuckDuckGo search component.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	duckduckgo "github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
)

const ComponentVersion = "v2.0.0-20261019121619-eee37f63ff95"

var ErrUnavailable = errors.New("web search unavailable")

type Request struct {
	Query     string `json:"query"`
	TimeRange string `json:"time_range,omitempty"`
}
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
}
type Response struct {
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Results []Result `json:"results"`
}
type Client struct{ search duckduckgo.Search }

// NewWithProxy isolates optional search proxy settings from other platform clients.
func NewWithProxy(ctx context.Context, proxyURL string) (*Client, error) {
	if proxyURL == "" {
		return New(ctx, nil)
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrUnavailable
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrUnavailable
	}
	transport := base.Clone()
	transport.Proxy = http.ProxyURL(u)
	return New(ctx, transport)
}

func New(ctx context.Context, transport http.RoundTripper) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if transport == nil {
		transport = http.DefaultTransport
		if base, ok := transport.(*http.Transport); ok {
			transport = base.Clone()
		}
	}
	httpClient := &http.Client{
		Transport: &boundedTransport{inner: transport}, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	search, err := duckduckgo.NewSearch(ctx, &duckduckgo.Config{HTTPClient: httpClient, MaxResults: 5, Region: duckduckgo.RegionWT})
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Client{search: search}, nil
}

func (c *Client) Search(ctx context.Context, request Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	response, err := c.search.TextSearch(ctx, &duckduckgo.TextSearchRequest{Query: request.Query, TimeRange: duckduckgo.TimeRange(request.TimeRange)})
	// The component's pagination delay is not interruptible. Never accept a late result.
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Response{}, context.DeadlineExceeded
		}
		return Response{}, ErrUnavailable
	}
	if response == nil {
		return Response{}, ErrUnavailable
	}
	result := Response{Status: "ok", Results: []Result{}}
	for _, r := range response.Results {
		if r != nil {
			result.Results = append(result.Results, Result{Title: r.Title, URL: r.URL, Summary: r.Summary})
		}
	}
	return Normalize(result), nil
}

// Normalize enforces the platform envelope even for a replacement search provider.
func Normalize(response Response) Response {
	output := Response{Status: response.Status, Results: []Result{}}
	if response.Status != "ok" && response.Status != "no_results" {
		switch response.Status {
		case "invalid_arguments":
			output.Message = "参数错误：query 必须为 1～500 个字符；time_range 只允许空、d、w、m、y，仅接受这两个字段。"
		case "search_timeout":
			output.Message = "此次联网搜索超时，未取得可用依据。请如实说明，可在任务预算内重试。"
		default:
			output.Status = "search_unavailable"
			output.Message = "此次联网搜索不可用，未取得可用依据。请如实说明，不要编造来源。"
		}
		return output
	}
	seen := map[string]bool{}
	if response.Status == "ok" {
		for _, r := range response.Results {
			link := strings.TrimSpace(r.URL)
			u, err := url.Parse(link)
			if err != nil || len(link) > 2048 || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(r.Title) == "" {
				continue
			}
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			link = u.String()
			if seen[link] {
				continue
			}
			seen[link] = true
			output.Results = append(output.Results, Result{Title: clip(r.Title, 200), URL: link, Summary: clip(r.Summary, 800)})
			if len(output.Results) == 5 {
				break
			}
		}
	}
	for {
		output.Status = "ok"
		output.Message = fmt.Sprintf("找到 %d 条公开网络搜索结果。仅为搜索摘要，未读取网页正文。", len(output.Results))
		if len(output.Results) == 0 {
			output.Status = "no_results"
			output.Message = "未找到可用的搜索结果，不要据此编造来源。"
		}
		encoded, _ := json.Marshal(output)
		if len(encoded) <= 16384 {
			return output
		}
		output.Results = output.Results[:len(output.Results)-1]
	}
}
func clip(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
