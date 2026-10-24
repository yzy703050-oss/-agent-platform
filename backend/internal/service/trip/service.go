package trip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

type Service struct {
	store          Store
	now            func() time.Time
	queries        QueryClient
	queryStore     QueryStore
	queryNamespace string
	queryGroup     singleflight.Group
	querySlots     chan struct{}
}

func New(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}
func (s *Service) Read(ctx context.Context, scope model.Scope, id string) (model.TripPlan, error) {
	if scope.UserID == "" || scope.SessionID == "" || id == "" {
		return model.TripPlan{}, model.ErrInvalidInput
	}
	return s.store.GetTrip(ctx, scope, id)
}

// Agent reads require eligible source data; the traditional Read API retains
// display-only historical access, including superseded tasks.
func (s *Service) readForExecution(ctx context.Context, ref model.TravelExecution, id string) (model.TripPlan, error) {
	if store, ok := s.store.(interface {
		GetAgentTrip(context.Context, model.TravelExecution, string) (model.TripPlan, error)
	}); ok {
		return store.GetAgentTrip(ctx, ref, id)
	}
	return s.Read(ctx, ref.Scope, id)
}
func (s *Service) Apply(ctx context.Context, ref model.TravelExecution, operationID string, cmd model.TripCommand) (model.TripResult, error) {
	if ref.Scope.UserID == "" || ref.Scope.SessionID == "" || ref.ConversationID == "" || ref.RunID == "" || ref.Attempt <= 0 || strings.TrimSpace(operationID) == "" {
		return model.TripResult{}, model.ErrInvalidInput
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		return model.TripResult{}, err
	}
	hash := sha256.Sum256(b)
	requestHash := hex.EncodeToString(hash[:])
	if result, ok, err := s.store.FindTripReceipt(ctx, ref, operationID, requestHash); err != nil || ok {
		return result, err
	}
	result, evidence, err := s.prepare(ctx, ref, cmd)
	if err != nil {
		return model.TripResult{}, err
	}
	if cmd.Operation == "propose" {
		result.Plan.ID = uuid.NewString()
		result.Plan.UserID = ref.Scope.UserID
		result.Plan.ConversationID = ref.ConversationID
	}
	result.Plan.SourceRunID = ref.RunID
	for i := range result.Queries {
		result.Queries[i].PlanID = result.Plan.ID
		result.Queries[i].PlanRevision = result.Plan.Revision
	}
	return s.store.CommitTrip(ctx, model.TripCommit{Execution: ref, OperationID: operationID, RequestHash: requestHash, ExpectedRevision: cmd.ExpectedRevision, Result: result, Evidence: evidence, Now: s.now()})
}

// Prepare checks the complete business mutation without accepting an action,
// generating persisted identities, calling suppliers or changing any row.
func (s *Service) Prepare(ctx context.Context, ref model.TravelExecution, cmd model.TripCommand) (model.TripResult, error) {
	result, _, err := s.prepare(ctx, ref, cmd)
	return result, err
}

func (s *Service) PrepareOperation(ctx context.Context, ref model.TravelExecution, id string, cmd model.TripCommand) (model.TripResult, error) {
	raw, err := json.Marshal(cmd)
	if err != nil {
		return model.TripResult{}, err
	}
	hash := sha256.Sum256(raw)
	if saved, ok, err := s.store.FindTripReceipt(ctx, ref, id, hex.EncodeToString(hash[:])); err != nil || ok {
		return saved, err
	}
	return s.Prepare(ctx, ref, cmd)
}
func (s *Service) prepare(ctx context.Context, ref model.TravelExecution, cmd model.TripCommand) (model.TripResult, []model.TravelEvidence, error) {
	if ref.Scope.UserID == "" || ref.Scope.SessionID == "" || ref.ConversationID == "" || ref.RunID == "" || ref.Attempt <= 0 {
		return model.TripResult{}, nil, model.ErrInvalidInput
	}
	var previous *model.TripPlan
	if cmd.Operation != "propose" {
		p, err := s.readForExecution(ctx, ref, cmd.PlanID)
		if err != nil {
			return model.TripResult{}, nil, err
		}
		if p.ConversationID != ref.ConversationID {
			return model.TripResult{}, nil, model.ErrNotFound
		}
		previous = &p
	}
	refs := make([]model.TravelCandidateRef, 0, len(cmd.Selections))
	for _, sel := range cmd.Selections {
		refs = append(refs, sel.Ref)
	}
	evidence, err := s.store.LoadTravelEvidence(ctx, ref, refs)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			if expired := expiredSelection(previous, cmd.Selections, s.now()); expired != nil {
				return model.TripResult{}, nil, expired
			}
		}
		return model.TripResult{}, nil, err
	}
	if cmd.Context != nil {
		for _, fact := range cmd.Context.EvidenceRefs {
			loaded, err := s.store.LoadTravelEvidence(ctx, ref, []model.TravelCandidateRef{fact})
			if errors.Is(err, model.ErrNotFound) {
				continue
			}
			if err != nil {
				return model.TripResult{}, nil, err
			}
			for _, e := range loaded {
				duplicate := false
				for _, old := range evidence {
					if old.ID == e.ID && old.ResultRevision == e.ResultRevision {
						duplicate = true
					}
				}
				if !duplicate {
					evidence = append(evidence, e)
				}
			}
		}
	}
	result, err := Preview(previous, cmd, evidence, s.now())
	if err != nil {
		return model.TripResult{}, nil, err
	}
	return result, evidence, nil
}

func clone[T any](in T) (T, error) {
	var out T
	b, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func invalid(detail string) error {
	code := "INVALID_ARGUMENTS"
	if head, body, ok := strings.Cut(detail, ": "); ok && slicesContainsCode(head) {
		code = head
		detail = body
	}
	return &model.TravelRuleError{Code: code, Message: detail}
}
func slicesContainsCode(code string) bool {
	switch code {
	case "REPLAN_REQUIRED", "QUERY_CONDITION_CONFLICT", "INVALID_REPLY_REFERENCE", "DEPARTURE_IN_PAST", "QUERY_REBIND_REQUIRED":
		return true
	}
	return false
}
