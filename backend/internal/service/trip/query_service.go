package trip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
)

const queryTTL = 5 * time.Minute

func NewWithQueries(store QueryStore, client QueryClient, now func() time.Time) *Service {
	s := New(store, now)
	s.queryStore = store
	s.queries = client
	s.querySlots = make(chan struct{}, 4)
	return s
}

type Providers map[string]QueryClient

func (p Providers) Search(ctx context.Context, q model.TravelQuery) (model.TravelEvidence, error) {
	client, ok := p[q.Domain]
	if !ok || client == nil {
		return model.TravelEvidence{Status: "unavailable", Message: "该查询供应方尚未配置。"}, nil
	}
	return client.Search(ctx, q)
}

func (s *Service) Query(ctx context.Context, ref model.TravelExecution, q model.TravelQuery) (model.TravelEvidence, error) {
	if s.queries == nil || s.queryStore == nil {
		return model.TravelEvidence{Status: "unavailable", Message: "差旅查询尚未配置。"}, nil
	}
	if err := normalizeQuery(&q); err != nil {
		return model.TravelEvidence{}, err
	}
	if q.PlanID != "" {
		p, err := s.store.GetTrip(ctx, ref.Scope, q.PlanID)
		if err != nil {
			return model.TravelEvidence{}, err
		}
		if p.ConversationID != ref.ConversationID || p.Revision != q.PlanRevision {
			return model.TravelEvidence{}, model.ErrConflict
		}
		matched := false
		for _, task := range p.Tasks {
			if task.ID != q.TaskID {
				continue
			}
			matched = true
			if q.Domain == "train" && (q.Origin != task.Origin || q.Destination != task.Destination || q.Date != task.DepartureDate) {
				return model.TravelEvidence{}, invalid("bound query route mismatch")
			}
			if q.Domain == "hotel" && (!task.RequiresHotel || q.City != task.Destination || q.CheckIn != task.CheckIn || q.CheckOut != task.CheckOut) {
				return model.TravelEvidence{}, invalid("bound query stay mismatch")
			}
		}
		if !matched {
			return model.TravelEvidence{}, model.ErrNotFound
		}
	}
	// Binding is checked separately: equal physical queries may reuse evidence,
	// but each returned reference is durably bound to its own plan/task/revision.
	physical := q
	physical.PlanID = ""
	physical.PlanRevision = 0
	physical.TaskID = ""
	data, err := json.Marshal(physical)
	if err != nil {
		return model.TravelEvidence{}, err
	}
	digest := sha256.Sum256(data)
	key := hex.EncodeToString(digest[:])
	scopeData, _ := json.Marshal([]string{ref.Scope.UserID, ref.Scope.SessionID, ref.ConversationID, ref.RunID, strconv.FormatInt(ref.Attempt, 10), key})
	value, err, _ := s.queryGroup.Do(string(scopeData), func() (any, error) {
		cached, ok, err := s.queryStore.FindTravelQuery(ctx, ref, key)
		if err != nil {
			return nil, err
		}
		if ok {
			return cached, nil
		}
		queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		select {
		case s.querySlots <- struct{}{}:
		case <-queryCtx.Done():
			return nil, queryCtx.Err()
		}
		defer func() { <-s.querySlots }()
		e, err := s.queries.Search(queryCtx, physical)
		if err != nil {
			return nil, err
		}
		if e.Status != "ok" && e.Status != "partial" {
			e.ID = ""
			e.Query = q
			return e, nil
		}
		if e.Source.Provider == "" || e.Source.FetchedAt.IsZero() || e.Source.FetchedAt.After(s.now()) || !e.Source.FetchedAt.Add(queryTTL).After(s.now()) {
			return nil, invalid("invalid query source or freshness")
		}
		e.ID = uuid.NewString()
		e.ResultRevision = 1
		e.Query = q
		e.PlanID = q.PlanID
		e.TaskID = q.TaskID
		e.UserID = ref.Scope.UserID
		e.ConversationID = ref.ConversationID
		e.SourceRunID = ref.RunID
		e.ExpiresAt = e.Source.FetchedAt.Add(queryTTL)
		return s.queryStore.SaveTravelQuery(queryCtx, ref, key, e)
	})
	if err != nil {
		return model.TravelEvidence{}, err
	}
	if err = ctx.Err(); err != nil {
		return model.TravelEvidence{}, err
	}
	e, err := clone(value.(model.TravelEvidence))
	if err != nil {
		return model.TravelEvidence{}, err
	}
	if e.ID == "" {
		e.Query = q
		return e, nil
	}
	if e.PlanID != q.PlanID || e.TaskID != q.TaskID || e.Query.PlanRevision != q.PlanRevision || e.SourceRunID != ref.RunID {
		e.ID = uuid.NewString()
		e.ResultRevision = 1
		e.Query = q
		e.PlanID = q.PlanID
		e.TaskID = q.TaskID
		e.SourceRunID = ref.RunID
		return s.queryStore.SaveTravelQuery(ctx, ref, key, e)
	}
	return e, nil
}

func normalizeQuery(q *model.TravelQuery) error {
	q.Origin = strings.TrimSpace(q.Origin)
	q.Destination = strings.TrimSpace(q.Destination)
	q.City = strings.TrimSpace(q.City)
	q.Keywords = strings.TrimSpace(q.Keywords)
	switch q.Domain {
	case "train":
		if q.Origin == "" || q.Destination == "" || q.Date == "" || q.Origin == q.Destination {
			return invalid("train query requires route and date")
		}
		if _, err := parseDate(q.Date); err != nil {
			return err
		}
	case "hotel":
		if q.City == "" {
			return invalid("hotel query requires city")
		}
		if (q.CheckIn == "") != (q.CheckOut == "") {
			return invalid("stay dates must be paired")
		}
		if q.CheckIn != "" {
			in, err := parseDate(q.CheckIn)
			if err != nil {
				return err
			}
			out, err := parseDate(q.CheckOut)
			if err != nil {
				return err
			}
			if !out.After(in) {
				return invalid("invalid hotel dates")
			}
		}
	case "weather":
		if q.City == "" {
			return invalid("weather query requires city")
		}
		if q.Date != "" {
			if _, err := parseDate(q.Date); err != nil {
				return err
			}
		}
	case "web":
		if q.Keywords == "" {
			return invalid("web query requires keywords")
		}
	default:
		return invalid("unsupported query domain")
	}
	if q.PlanID != "" && (q.PlanRevision <= 0 || q.TaskID == "") {
		return invalid("bound query requires plan revision and task")
	}
	return validateConstraints(q.Constraints)
}
