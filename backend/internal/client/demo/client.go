// Package demo provides explicitly labelled read-only fixtures. It performs no network IO.
package demo

import (
	"agent-platform/backend/internal/model"
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

type Client struct{ now func() time.Time }

func New(now func() time.Time) *Client { return &Client{now: now} }

func (c *Client) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	if err := ctx.Err(); err != nil {
		return model.TravelEvidence{}, err
	}
	out := model.TravelEvidence{IsDemo: true, Status: "ok", Source: model.TravelSource{Provider: "平台演示供应方", FetchedAt: c.now()}, Message: "演示数据：仅用于功能演示，不代表真实车次、报价、余票或天气。"}
	switch q.Domain {
	case "train":
		loc := time.FixedZone("CST", 8*60*60)
		date, err := time.ParseInLocation("2006-01-02", q.Date, loc)
		if err != nil {
			return model.TravelEvidence{}, model.ErrInvalidInput
		}
		digest := sha256.Sum256([]byte(q.Origin + "|" + q.Destination + "|" + q.Date))
		for i, amount := range []int64{10000, 15000} {
			depart := date.Add(time.Duration(9+i*5) * time.Hour)
			arrive := depart.Add(2 * time.Hour)
			out.Candidates = append(out.Candidates, model.TravelCandidate{IsDemo: true, ID: fmt.Sprintf("demo-%x-%d", digest[:6], i), Name: fmt.Sprintf("演示 G%d", 1001+i), Origin: q.Origin, Destination: q.Destination, DepartureAt: &depart, ArrivalAt: &arrive, PriceFen: &amount, TrainKind: "G", SeatClass: "二等座", Availability: "unknown"})
		}
	case "hotel":
		out.Status = "partial"
		out.MissingFields = []string{"price_fen", "availability"}
		out.Candidates = []model.TravelCandidate{{IsDemo: true, ID: "demo-hotel-1", Name: q.City + "演示酒店地点", City: q.City, Address: q.City + "演示商务区（虚构地址）", Availability: "unknown"}}
	case "weather":
		temp := 22.0
		out.Weather = []model.TravelWeather{{Date: q.Date, Description: q.City + "演示晴天（非真实预报）", TemperatureC: &temp}}
	default:
		return model.TravelEvidence{Status: "unavailable", Message: "该查询供应方尚未配置。"}, nil
	}
	return out, nil
}
