package train

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
)

const endpoint = "https://apis.juhe.cn/fapigw/train/query"
const documentation = "https://www.juhe.cn/docs/api/id/817"

var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

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
	e := model.TravelEvidence{Status: "unavailable", Query: q}
	if c.key == "" {
		e.Message = "未配置车次查询凭据。"
		return e, nil
	}
	date, err := time.ParseInLocation("2006-01-02", q.Date, beijing)
	if err != nil {
		return e, model.ErrInvalidInput
	}
	today := c.now().In(beijing).Format("2006-01-02")
	start, _ := time.ParseInLocation("2006-01-02", today, beijing)
	if date.Before(start) || !date.Before(start.AddDate(0, 0, 15)) {
		e.Message = "车次供应方仅支持今天起 15 个日历日的查询。"
		return e, nil
	}
	values := url.Values{"key": {c.key}, "search_type": {"1"}, "enable_booking": {"2"}, "departure_station": {q.Origin}, "arrival_station": {q.Destination}, "date": {q.Date}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return e, providerhttp.ErrRequest
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var payload struct {
		ErrorCode *int `json:"error_code"`
		Result    []struct {
			Number      string `json:"train_no"`
			Origin      string `json:"departure_station"`
			Destination string `json:"arrival_station"`
			Departure   string `json:"departure_time"`
			Arrival     string `json:"arrival_time"`
			Duration    string `json:"duration"`
			Booking     string `json:"enable_booking"`
			Prices      []struct {
				Name      string          `json:"seat_name"`
				Code      string          `json:"seat_type_code"`
				Price     json.RawMessage `json:"price"`
				Remaining json.RawMessage `json:"num"`
			} `json:"prices"`
		} `json:"result"`
	}
	if err = providerhttp.Decode(ctx, c.http, req, &payload); err != nil {
		return e, err
	}
	if payload.ErrorCode == nil {
		return e, providerhttp.ErrResponse
	}
	if *payload.ErrorCode != 0 {
		e.Message = "车次查询服务不可用，请检查凭据、权限或配额。"
		return e, nil
	}
	if payload.Result == nil {
		return e, providerhttp.ErrResponse
	}
	e.Status = "ok"
	e.Source = model.TravelSource{Provider: "聚合数据", URL: documentation, FetchedAt: c.now()}
	for _, row := range payload.Result {
		if !c.safe(row.Number) || !c.safe(row.Origin) || !c.safe(row.Destination) || row.Origin != q.Origin || row.Destination != q.Destination {
			e.Status = "partial"
			continue
		}
		departure, err := time.ParseInLocation("2006-01-02 15:04", q.Date+" "+row.Departure, beijing)
		if err != nil {
			e.Status = "partial"
			continue
		}
		var arrival *time.Time
		parts := strings.Split(row.Duration, ":")
		if len(parts) == 2 {
			h, herr := strconv.Atoi(parts[0])
			m, merr := strconv.Atoi(parts[1])
			if herr == nil && merr == nil && h >= 0 && h <= 999 && m >= 0 && m < 60 {
				candidate := departure.Add(time.Duration(h*60+m) * time.Minute)
				if candidate.Format("15:04") == row.Arrival {
					arrival = &candidate
				}
			}
		}
		for _, seat := range row.Prices {
			price, ok := priceFen(seat.Price)
			if !ok || !c.safe(seat.Name) {
				e.Status = "partial"
				continue
			}
			identity, _ := json.Marshal([]string{q.Date, row.Number, row.Origin, row.Destination, seat.Code, seat.Name})
			digest := sha256.Sum256(identity)
			remaining, availability := inventory(seat.Remaining, row.Booking)
			kind := sourceTrainKind(row.Number)
			e.Candidates = append(e.Candidates, model.TravelCandidate{ID: hex.EncodeToString(digest[:]), Name: row.Number, Origin: row.Origin, Destination: row.Destination, DepartureAt: &departure, ArrivalAt: arrival, PriceFen: &price, TrainKind: kind, SeatClass: seat.Name, Availability: availability, Remaining: remaining})
		}
	}
	if e.Status == "partial" {
		e.Message = "部分车次或席别字段无效，已排除。"
	}
	if len(e.Candidates) == 0 && e.Message == "" {
		e.Message = "未找到匹配车次，请核对日期与具体车站。"
	}
	return e, nil
}
func (c *Client) safe(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.Contains(value, c.key)
}
func priceFen(raw json.RawMessage) (int64, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		text = string(raw)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return r.Num().Int64(), true
}
func inventory(raw json.RawMessage, booking string) (*int64, string) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		text = string(raw)
	}
	var remaining *int64
	if text == "无" {
		zero := int64(0)
		remaining = &zero
	} else if n, err := strconv.ParseInt(text, 10, 64); err == nil && n >= 0 {
		remaining = &n
	}
	if remaining != nil && *remaining == 0 {
		return remaining, "sold_out"
	}
	if booking == "N" {
		return remaining, "unavailable"
	}
	if booking == "Y" && (text == "有" || (remaining != nil && *remaining > 0)) {
		return remaining, "available"
	}
	return remaining, "unknown"
}
