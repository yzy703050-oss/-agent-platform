package weather

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
)

type Client struct {
	http providerhttp.Doer
	now  func() time.Time
}

func New(client providerhttp.Doer, now func() time.Time) *Client {
	if now == nil {
		now = time.Now
	}
	return &Client{http: providerhttp.Default(client), now: now}
}

type description struct {
	Value string `json:"value"`
}

func (c *Client) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	endpoint := "https://wttr.in/" + url.PathEscape(q.City) + "?format=j1"
	e := model.TravelEvidence{Status: "partial", Query: q, Source: model.TravelSource{Provider: "wttr.in", URL: endpoint, FetchedAt: c.now()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return e, providerhttp.ErrRequest
	}
	req.Header.Set("User-Agent", "agent-platform/1.0")
	var payload struct {
		Current []struct {
			Temperature string        `json:"temp_C"`
			Description []description `json:"weatherDesc"`
		} `json:"current_condition"`
		Weather []struct {
			Date   string `json:"date"`
			Min    string `json:"mintempC"`
			Max    string `json:"maxtempC"`
			Hourly []struct {
				Description []description `json:"weatherDesc"`
			} `json:"hourly"`
		} `json:"weather"`
	}
	if err = providerhttp.Decode(ctx, c.http, req, &payload); err != nil {
		return e, err
	}
	if payload.Current == nil && payload.Weather == nil {
		return e, providerhttp.ErrResponse
	}
	if q.Date == "" && len(payload.Current) > 0 {
		row := payload.Current[0]
		temperature, ok := number(row.Temperature)
		if !ok {
			return e, providerhttp.ErrResponse
		}
		desc := ""
		if len(row.Description) > 0 {
			desc = row.Description[0].Value
		}
		e.Weather = append(e.Weather, model.TravelWeather{TemperatureC: &temperature, Description: desc})
	} else {
		for _, row := range payload.Weather {
			if row.Date != q.Date {
				continue
			}
			min, minOK := number(row.Min)
			max, maxOK := number(row.Max)
			if !minOK || !maxOK || min > max {
				return e, providerhttp.ErrResponse
			}
			desc := ""
			if len(row.Hourly) > 0 && len(row.Hourly[0].Description) > 0 {
				desc = row.Hourly[0].Description[0].Value
			}
			e.Weather = append(e.Weather, model.TravelWeather{Date: q.Date, Description: desc, MinTemperatureC: &min, MaxTemperatureC: &max})
		}
	}
	if len(e.Weather) > 0 {
		e.Status = "ok"
	} else {
		e.Message = "天气接口没有返回所请求日期的数据。"
	}
	return e, nil
}
func number(text string) (float64, bool) {
	v, err := strconv.ParseFloat(text, 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
