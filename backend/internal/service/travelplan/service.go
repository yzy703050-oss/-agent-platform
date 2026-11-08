package travelplan

import (
	"context"
	"strings"

	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/model"
	v1 "agent-platform/backend/internal/model/runtimev1"
)

type Store interface {
	ReplayPlanAction(context.Context, v1.ExecRef, string, string, any) (v1.CommitResult, bool, error)
	ImportedPlanSources(context.Context, v1.ExecRef, v1.PlanDefinition) ([]v1.EvidenceIdentity, error)
	CreatePlan(context.Context, v1.ExecRef, string, v1.PlanDefinition) (v1.CommitResult, error)
	PatchPlan(context.Context, v1.ExecRef, string, v1.PlanPatch) (v1.CommitResult, error)
	ViewPlan(context.Context, v1.ExecRef, string) (v1.PlanView, error)
	AcceptPlanQuery(context.Context, v1.ExecRef, v1.ExecutionRequest, evidence.View) (v1.ExecutionResult, error)
}
type EvidenceReader interface {
	Resolve(context.Context, v1.ExecRef, string, int64) (evidence.View, error)
}
type Service struct {
	store  Store
	reader EvidenceReader
}

func New(store Store) *Service { return &Service{store: store} }
func NewWithEvidence(store Store, reader EvidenceReader) *Service {
	return &Service{store: store, reader: reader}
}
func validOperation(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 160 }

func (s *Service) refreshSources(ctx context.Context, ref v1.ExecRef, definition v1.PlanDefinition) error {
	refs, err := s.store.ImportedPlanSources(ctx, ref, definition)
	if err != nil {
		return err
	}
	seen := map[v1.EvidenceIdentity]bool{}
	for _, identity := range refs {
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if s.reader == nil {
			return model.ErrInvalidInput
		}
		if _, err = s.reader.Resolve(ctx, ref, identity.EvidenceID, identity.ResultRevision); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Create(ctx context.Context, ref v1.ExecRef, id string, definition v1.PlanDefinition) (v1.CommitResult, error) {
	if s.store == nil || !validOperation(id) {
		return v1.CommitResult{}, model.ErrInvalidInput
	}
	if err := ValidateDefinition(definition); err != nil {
		return v1.CommitResult{}, err
	}
	if prior, exists, err := s.store.ReplayPlanAction(ctx, ref, id, "create_plan", definition); err != nil {
		return prior, err
	} else if exists {
		return prior, nil
	}
	if err := s.refreshSources(ctx, ref, definition); err != nil {
		return v1.CommitResult{}, err
	}
	return s.store.CreatePlan(ctx, ref, id, definition)
}
func (s *Service) ApplyPatch(ctx context.Context, ref v1.ExecRef, id string, patch v1.PlanPatch) (v1.CommitResult, error) {
	if s.store == nil || !validOperation(id) {
		return v1.CommitResult{}, model.ErrInvalidInput
	}
	var out v1.PlanPatch
	if err := checked(patch, "PlanPatch", &out); err != nil {
		return v1.CommitResult{}, err
	}
	if prior, exists, err := s.store.ReplayPlanAction(ctx, ref, id, "apply_patch", out); err != nil {
		return prior, err
	} else if exists {
		return prior, nil
	}
	base, err := s.store.ViewPlan(ctx, ref, out.PlanID)
	if err != nil {
		return v1.CommitResult{}, err
	}
	change, err := ProposePatch(base.Snapshot, out)
	if err != nil {
		return v1.CommitResult{}, err
	}
	if err = s.refreshSources(ctx, ref, change.Snapshot.Definition); err != nil {
		return v1.CommitResult{}, err
	}
	seen := map[v1.EvidenceIdentity]bool{}
	for _, selection := range change.Snapshot.Selections {
		if !strings.HasPrefix(selection.Evidence.EvidenceID, "remote:") {
			continue
		}
		identity := v1.EvidenceIdentity{EvidenceID: selection.Evidence.EvidenceID, ResultRevision: selection.Evidence.ResultRevision}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if s.reader == nil {
			return v1.CommitResult{}, model.ErrInvalidInput
		}
		if _, err = s.reader.Resolve(ctx, ref, identity.EvidenceID, identity.ResultRevision); err != nil {
			return v1.CommitResult{}, err
		}
	}
	return s.store.PatchPlan(ctx, ref, id, out)
}
func (s *Service) View(ctx context.Context, ref v1.ExecRef, id string) (v1.PlanView, error) {
	if s.store == nil || strings.TrimSpace(id) == "" {
		return v1.PlanView{}, model.ErrInvalidInput
	}
	return s.store.ViewPlan(ctx, ref, id)
}
func (s *Service) AcceptQueryResult(ctx context.Context, ref v1.ExecRef, request v1.ExecutionRequest, view evidence.View) (v1.ExecutionResult, error) {
	if s.store == nil {
		return v1.ExecutionResult{}, model.ErrInvalidInput
	}
	var out v1.ExecutionRequest
	if err := checked(request, "ExecutionRequest", &out); err != nil {
		return v1.ExecutionResult{}, err
	}
	current, err := s.store.ViewPlan(ctx, ref, out.PlanID)
	if err != nil {
		return v1.ExecutionResult{}, err
	}
	for _, version := range current.Snapshot.TaskVersions {
		if version.TaskID == out.TaskID && version.TaskRevision == out.TaskRevision {
			if err = s.refreshSources(ctx, ref, current.Snapshot.Definition); err != nil {
				return v1.ExecutionResult{}, err
			}
			break
		}
	}
	if strings.HasPrefix(view.ReferenceID, "remote:") {
		if s.reader == nil {
			return v1.ExecutionResult{}, model.ErrInvalidInput
		}
		fresh, err := s.reader.Resolve(ctx, ref, view.ReferenceID, view.ResultRevision)
		if err != nil {
			return v1.ExecutionResult{}, err
		}
		a, e1 := canonical(fresh)
		b, e2 := canonical(view)
		if e1 != nil || e2 != nil || a != b {
			return v1.ExecutionResult{}, model.ErrConflict
		}
		view = fresh
	}
	return s.store.AcceptPlanQuery(ctx, ref, out, view)
}
