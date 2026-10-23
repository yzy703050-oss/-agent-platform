package hotel

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
)

const endpoint = "https://restapi.amap.com/v5/place/text"
const documentation = "https://developer.amap.com/api/webservice/guide/api-advanced/newpoisearch"

type Client struct {
	key  string
	http providerhttp.Doer
	now  func() time.Time
}

func New(key string, client providerhttp.Doer, now func() time.Time) *Client {
	if now == nil {
		now = time.Now
	}
	return &Client{key: strings.TrimSpace(key), http: providerhttp.Default(client), now: now}
}
func (c *Client) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	e := model.TravelEvidence{Status: "unavailable", Query: q, Message: "酒店地点候选不包含入住日期对应的房价、房型或空房信息。"}
	if c.key == "" {
		e.Message = "未配置酒店地点查询凭据。"
		return e, nil
	}
	keywords := q.Keywords
	if keywords == "" {
		keywords = "酒店"
	}
	values := url.Values{"key": {c.key}, "keywords": {keywords}, "types": {"100000"}, "region": {q.City}, "city_limit": {"true"}, "page_size": {"25"}, "page_num": {"1"}, "show_fields": {"business,photos"}, "output": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return e, providerhttp.ErrRequest
	}
	var payload struct {
		Status   string `json:"status"`
		InfoCode string `json:"infocode"`
		Pois     []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"typecode"`
			City    string `json:"cityname"`
			Address string `json:"address"`
		} `json:"pois"`
	}
	if err = providerhttp.Decode(ctx, c.http, req, &payload); err != nil {
		return e, err
	}
	if payload.Status != "1" || payload.InfoCode != "10000" {
		return e, providerhttp.ErrResponse
	}
	if payload.Pois == nil {
		return e, providerhttp.ErrResponse
	}
	e.Status = "ok"
	e.Source = model.TravelSource{Provider: "高德地图", URL: documentation, FetchedAt: c.now()}
	for _, p := range payload.Pois {
		if p.ID == "" || p.Name == "" || !strings.HasPrefix(p.Type, "10") || strings.TrimSuffix(p.City, "市") != strings.TrimSuffix(q.City, "市") || strings.Contains(p.ID, c.key) || strings.Contains(p.Name, c.key) || strings.Contains(p.Address, c.key) {
			e.Status = "partial"
			continue
		}
		e.Candidates = append(e.Candidates, model.TravelCandidate{ID: p.ID, Name: p.Name, City: q.City, Address: p.Address, PriceFen: nil})
	}
	return e, nil
}
