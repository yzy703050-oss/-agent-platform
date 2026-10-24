package travel

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	pm "agent-platform/backend/internal/model"
	tripsvc "agent-platform/backend/internal/service/trip"
)

func sourceRelatedFacts(results []pm.TravelToolResult) string {
	type view struct {
		Domain     string                       `json:"domain"`
		Query      json.RawMessage              `json:"query"`
		Parameters json.RawMessage              `json:"parameters"`
		QueryID    string                       `json:"query_id"`
		Items      []map[string]json.RawMessage `json:"items"`
		Message    string                       `json:"message"`
		Reason     string                       `json:"reason"`
		IsDemo     bool                         `json:"is_demo"`
	}
	latest := []view{}
	indexes := map[string]int{}
	for _, r := range results {
		if r.Name != "query" {
			continue
		}
		var rows []view
		if json.Unmarshal(r.Data, &rows) != nil {
			continue
		}
		for _, row := range rows {
			params := row.Query
			if len(params) == 0 {
				params = row.Parameters
			}
			var canonical any
			if json.Unmarshal(params, &canonical) == nil {
				params, _ = json.Marshal(canonical)
			}
			key := row.Domain + ":" + string(params)
			if index, ok := indexes[key]; ok {
				latest[index] = row
			} else {
				indexes[key] = len(latest)
				latest = append(latest, row)
			}
		}
	}
	sections := []string{}
	text := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	for _, v := range latest {
		label := map[string]string{"weather": "天气", "hotel": "酒店候选", "train": "铁路候选", "guide": "资料"}[v.Domain]
		if label == "" {
			label = "查询"
		}
		var query map[string]json.RawMessage
		_ = json.Unmarshal(v.Query, &query)
		if city := text(query["city"]); city != "" {
			label = city + label
		} else if from, to := text(query["origin"]), text(query["destination"]); from != "" && to != "" {
			label = from + " → " + to + label
		}
		lines := []string{}
		demo := v.IsDemo
		for _, item := range v.Items {
			values := []string{}
			for _, key := range []string{"date", "description", "title", "snippet", "hotel_name", "address", "train_number", "departure_at", "arrival_at", "seat_class"} {
				if value := text(item[key]); value != "" {
					values = append(values, value)
				}
			}
			if raw := item["price_cny"]; len(raw) > 0 && string(raw) != "null" {
				if fen, err := tripsvc.MoneyFen(raw); err == nil {
					values = append(values, fmt.Sprintf("票价：%d.%02d元", fen/100, fen%100))
				}
			}
			for _, field := range []struct{ key, label string }{{"temp_c", "气温"}, {"min_temp_c", "最低气温"}, {"max_temp_c", "最高气温"}} {
				var value float64
				if raw := item[field.key]; len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &value) == nil {
					values = append(values, fmt.Sprintf("%s：%.1f℃", field.label, value))
				}
			}
			if url := text(item["url"]); url != "" {
				values = append(values, url)
			}
			var itemDemo bool
			_ = json.Unmarshal(item["is_demo"], &itemDemo)
			demo = demo || itemDemo
			if len(values) > 0 {
				lines = append(lines, strings.Join(values, "，"))
			}
		}
		if len(lines) == 0 {
			message := v.Message
			if message == "" {
				message = v.Reason
			}
			if message == "" {
				message = "本次查询没有可展示结果。"
			}
			lines = append(lines, message)
		}
		if demo {
			lines = append([]string{"演示数据：仅供功能验证。"}, lines...)
		}
		sections = append(sections, label+"：\n"+strings.Join(lines, "\n"))
	}
	for _, r := range results {
		if r.Name == "write_memory" && r.Status == "ok" {
			var data struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(r.Data, &data)
			if data.Status != "no_samples" {
				sections = append(sections, "长期偏好已更新。")
				break
			}
		}
	}
	return strings.Join(sections, "\n\n")
}

func unsupportedBookingClaim(answer string) bool {
	return slices.ContainsFunc([]string{"已付款", "已经付款", "已支付", "已经支付", "付款成功", "支付成功", "预订成功", "预定成功", "已预订", "已经预订", "已订票", "已出票", "出票成功", "已购买"}, func(claim string) bool { return strings.Contains(answer, claim) })
}

func sourcePresentation(answer string, s FlowState) (string, error) {
	bound := false
	for _, r := range s.Results {
		if r.RenderedAnswer != "" && strings.Contains(answer, r.RenderedAnswer) {
			bound = true
		}
	}
	incomplete := false
	for _, p := range s.Plans {
		incomplete = incomplete || p.Validation.Status != "complete"
	}
	replaced := false
	if len(s.Plans) > 0 && (!bound && incomplete || unsupportedBookingClaim(answer)) {
		var err error
		answer, err = stoppedSourceReply(s)
		if err != nil {
			return "", err
		}
		replaced = true
	}
	related := sourceRelatedFacts(s.Results)
	if unsupportedBookingClaim(answer) {
		if related != "" {
			return related, nil
		}
		return "本次没有预订或付款操作。", nil
	}
	if (bound || replaced || s.StopReason != "") && related != "" {
		answer += "\n\n" + related
	}
	return answer, nil
}

func (f *sourceFlow) finalize(ctx context.Context, answer string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load(ctx)
	if err != nil {
		return "", err
	}
	if (s.Phase == "completed" || s.Phase == "stopped") && s.FinalText != "" {
		return s.FinalText, nil
	}
	answer, err = sourcePresentation(answer, s)
	if err != nil {
		return "", err
	}
	s.FinalText = answer
	s.Phase = "completed"
	if s.StopReason != "" {
		s.Phase = "stopped"
	}
	return answer, f.save(ctx, s)
}
