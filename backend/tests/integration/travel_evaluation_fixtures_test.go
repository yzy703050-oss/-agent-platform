package integration

import (
	pm "agent-platform/backend/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

func newPerfProvider(now func() time.Time) *perfProvider {
	return &perfProvider{inner: &perfFixtureClient{now: now}, Calls: map[string]int{}}
}

// Fixture provenance belongs to the report, never to the business observation.
// This provider is only used by the opt-in benchmark, not by production demo mode.
type perfFixtureClient struct{ now func() time.Time }

func (c *perfFixtureClient) Search(ctx context.Context, q pm.TravelQuery) (pm.TravelEvidence, error) {
	if err := ctx.Err(); err != nil {
		return pm.TravelEvidence{}, err
	}
	e := pm.TravelEvidence{Status: "ok", Source: pm.TravelSource{Provider: "travel_supplier", FetchedAt: c.now()}}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	switch q.Domain {
	case "train":
		date, err := time.ParseInLocation(time.DateOnly, q.Date, loc)
		if err != nil {
			return e, pm.ErrInvalidInput
		}
		digest := sha256.Sum256([]byte(q.Origin + "|" + q.Destination + "|" + q.Date))
		for i, price := range []int64{10000, 15000} {
			depart := date.Add(time.Duration(9+i*5) * time.Hour)
			arrive := depart.Add(2 * time.Hour)
			stock := int64(12)
			e.Candidates = append(e.Candidates, pm.TravelCandidate{ID: fmt.Sprintf("rail_%x_%d", digest[:6], i), Name: fmt.Sprintf("G%d", 1001+i), Origin: q.Origin, Destination: q.Destination, DepartureAt: &depart, ArrivalAt: &arrive, PriceFen: &price, TrainKind: "G", SeatClass: "二等座", Availability: "available", Remaining: &stock})
		}
	case "hotel":
		// The production places projection removes stay dates. These benchmark
		// scenarios all stay one night; their city quote is fixed for that stay.
		// This fixture does not model date-sensitive or multi-night inventory.
		price := int64(30000)
		stock := int64(6)
		digest := sha256.Sum256([]byte(q.City))
		e.Candidates = []pm.TravelCandidate{{ID: fmt.Sprintf("hotel_%x", digest[:6]), Name: q.City + "商务酒店", City: q.City, Address: q.City + "商务区", PriceFen: &price, Availability: "available", Remaining: &stock}}
	case "weather":
		temp := 22.0
		e.Weather = []pm.TravelWeather{{Date: q.Date, Description: q.City + "晴", TemperatureC: &temp}}
	default:
		return pm.TravelEvidence{Status: "unavailable", Message: "该查询服务未配置。"}, nil
	}
	return e, nil
}

type perfPolicy struct{}

func (perfPolicy) Search(context.Context, string) (pm.PolicyEvidence, error) {
	return pm.PolicyEvidence{Status: "ok", Collection: "travel_policy", Fragments: []pm.PolicyFragment{{ID: "policy-rail-hotel-v1", Content: "员工差旅优先高铁二等座，每段交通费用上限500元。杭州及南京住宿费用上限为每晚400元。报销以实际费用和有效凭证为准。", Source: "差旅管理制度v1", Metadata: map[string]json.RawMessage{"document_id": json.RawMessage(`"travel-policy"`), "version": json.RawMessage(`"v1"`), "effective_date": json.RawMessage(`"2026-01-01"`)}}}}, nil
}
