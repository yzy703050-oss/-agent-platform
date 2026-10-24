package trip

import (
	"context"
	"fmt"
	"time"

	"agent-platform/backend/internal/model"
)

func (s *Service) searchSource(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	if q.Domain != "train" || q.ArrivalDate == "" {
		return s.queries.Search(ctx, q)
	}
	target, err := parseDate(q.ArrivalDate)
	if err != nil {
		return model.TravelEvidence{}, err
	}
	dates := []string{q.Date}
	if q.Date == "" {
		dates = nil
		for i := 0; i < 3; i++ {
			dates = append(dates, target.AddDate(0, 0, -i).Format(time.DateOnly))
		}
	}
	var last model.TravelEvidence
	for i, day := range dates {
		actual := q
		actual.Date = day
		actual.ArrivalDate = ""
		actual.ArrivalBefore = ""
		last, err = s.queries.Search(ctx, actual)
		if err != nil {
			return last, err
		}
		if last.Status == "error" || last.Status == "unavailable" {
			last.Candidates = nil
			return last, nil
		}
		unknown := false
		items := []model.TravelCandidate{}
		for _, c := range last.Candidates {
			if c.DepartureAt == nil || c.ArrivalAt == nil {
				unknown = true
				continue
			}
			if c.DepartureAt.In(beijing).Format(time.DateOnly) != day || c.ArrivalAt.In(beijing).Format(time.DateOnly) != q.ArrivalDate || c.ArrivalAt.Before(*c.DepartureAt) {
				continue
			}
			if q.ArrivalBefore != "" {
				deadline, _ := time.Parse(time.RFC3339, q.ArrivalBefore)
				if c.ArrivalAt.After(deadline) {
					continue
				}
			}
			items = append(items, c)
		}
		last.Candidates = items
		// Eligibility may include caller filters, but the cached physical result must
		// retain all valid arrival matches so other projections can reuse it.
		if len(items) > 0 {
			last.Message = fmt.Sprintf("按目标抵达日期筛选，已查询%d个出发日期；来源见候选。", i+1)
			return last, nil
		}
		if unknown {
			last.Status = "partial"
			last.Message = "车次抵达日期证据不足。"
			return last, nil
		}
	}
	last.Message = fmt.Sprintf("已查%d个出发日期，未取得符合目标抵达日期的候选；搜索范围有限。", len(dates))
	return last, nil
}
