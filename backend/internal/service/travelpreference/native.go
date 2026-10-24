package travelpreference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
)

type MemoryBatchStore interface {
	CommitTravelMemory(context.Context, model.TravelMemoryCommit) (model.TravelMemoryResult, error)
}

// PrepareNativeMemory applies source update semantics to a private snapshot.
// Repository commits the final changes, partial outcome and receipt together.
func PrepareNativeMemory(rows []model.TravelPreference, updates []model.TravelMemoryUpdate, analysis *model.TravelPreferenceAnalysis) (model.TravelMemoryCommit, error) {
	commit := model.TravelMemoryCommit{ExpectedVersions: map[string]int64{}, Result: model.TravelMemoryResult{Applied: []model.TravelMemoryApplied{}, Rejected: []model.TravelMemoryUpdate{}}}
	memories := map[string]model.TravelPreference{}
	ordered := []string{}
	var previous *model.TravelPreferenceAnalysis
	for _, row := range rows {
		commit.ExpectedVersions[row.Key] = row.Revision
		if strings.HasPrefix(row.Key, "memory_") {
			memories[row.Key] = row
			ordered = append(ordered, row.Key)
		}
		if row.Key == "$analysis" {
			var a model.TravelPreferenceAnalysis
			if json.Unmarshal([]byte(row.Value), &a) == nil {
				previous = &a
			}
		}
	}
	changes := map[string]model.TravelPreferenceCommand{}
	changedOrder := []string{}
	for _, update := range updates {
		identity := ""
		if update.ID != nil {
			identity = *update.ID
		}
		existing, exists := memories[identity]
		text := ""
		if update.Text != nil {
			text = strings.TrimSpace(*update.Text)
		}
		valid := slices.Contains([]string{"add", "replace", "retract"}, update.Op) && slices.Contains([]string{"explicit", "history"}, update.Source) && (update.Op == "retract" || text != "") && (update.Op == "add" || exists)
		protected := exists && update.Source == "history" && (existing.Origin == "explicit" || existing.Retracted)
		if update.Source == "history" {
			for _, row := range memories {
				if row.Retracted && row.Value == text {
					protected = true
				}
			}
		}
		if !valid || protected {
			commit.Result.Rejected = append(commit.Result.Rejected, update)
			continue
		}
		if update.Op == "add" {
			identity = "memory_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			existing = model.TravelPreference{Key: identity}
			ordered = append(ordered, identity)
		}
		if update.Op == "retract" {
			existing.Retracted = true
		} else {
			existing.Value = text
			existing.Retracted = false
			existing.Origin = map[string]string{"explicit": "explicit", "history": "inferred"}[update.Source]
		}
		memories[identity] = existing
		operation := "set"
		if existing.Retracted {
			operation = "retract"
		}
		if _, ok := changes[identity]; !ok {
			changedOrder = append(changedOrder, identity)
		}
		changes[identity] = model.TravelPreferenceCommand{Key: identity, Value: existing.Value, Origin: existing.Origin, Operation: operation}
		commit.Result.Applied = append(commit.Result.Applied, model.TravelMemoryApplied{Op: update.Op, ID: identity})
	}
	for _, key := range changedOrder {
		commit.Changes = append(commit.Changes, changes[key])
	}
	if analysis != nil || previous != nil {
		next := model.TravelPreferenceAnalysis{}
		if previous != nil {
			next = *previous
			next.CoveredTripIDs = slices.Clone(previous.CoveredTripIDs)
		}
		if analysis != nil {
			next.InitialSummaryDone = analysis.InitialSummaryDone
			next.Trigger = analysis.Trigger
			for _, id := range analysis.CoveredTripIDs {
				if !slices.Contains(next.CoveredTripIDs, id) {
					next.CoveredTripIDs = append(next.CoveredTripIDs, id)
				}
			}
		}
		next.MemoryIDs = []string{}
		texts := []string{}
		for _, id := range ordered {
			m := memories[id]
			if m.Origin == "inferred" && !m.Retracted {
				next.MemoryIDs = append(next.MemoryIDs, id)
				texts = append(texts, m.Value)
			}
		}
		next.Summary = strings.Join(texts, "；")
		slices.Sort(next.CoveredTripIDs)
		raw, _ := json.Marshal(next)
		commit.Changes = append(commit.Changes, model.TravelPreferenceCommand{Key: "$analysis", Value: string(raw), Operation: "set", Origin: "inferred"})
	}
	return commit, nil
}

func (s *Service) Update(ctx context.Context, ref model.TravelExecution, id string, cmd model.TravelMemoryCommand) (model.TravelMemoryResult, error) {
	if cmd.Operation != "update" || len(cmd.Updates) == 0 {
		return model.TravelMemoryResult{}, model.ErrInvalidInput
	}
	cmd.Updates = slices.Clone(cmd.Updates)
	for i := range cmd.Updates {
		cmd.Updates[i].Source = "explicit"
	}
	return s.commitMemory(ctx, ref, id, cmd.Updates, nil, nil)
}
func (s *Service) SaveAnalysis(ctx context.Context, ref model.TravelExecution, id string, updates []model.TravelMemoryUpdate, analysis model.TravelPreferenceAnalysis, sourceRuns []string) (model.TravelMemoryResult, error) {
	updates = slices.Clone(updates)
	for i := range updates {
		updates[i].Source = "history"
	}
	return s.commitMemory(ctx, ref, id, updates, &analysis, sourceRuns)
}
func (s *Service) commitMemory(ctx context.Context, ref model.TravelExecution, id string, updates []model.TravelMemoryUpdate, analysis *model.TravelPreferenceAnalysis, sourceRuns []string) (model.TravelMemoryResult, error) {
	if id == "" || ref.Scope.UserID == "" || ref.Scope.SessionID == "" || ref.RunID == "" || ref.ConversationID == "" || ref.Attempt <= 0 {
		return model.TravelMemoryResult{}, model.ErrInvalidInput
	}
	store, ok := s.store.(MemoryBatchStore)
	if !ok {
		return model.TravelMemoryResult{}, model.ErrAgentUnavailable
	}
	rows, err := s.ReadEffective(ctx, ref.Scope, ref.RunID)
	if err != nil {
		return model.TravelMemoryResult{}, err
	}
	commit, err := PrepareNativeMemory(rows, updates, analysis)
	if err != nil {
		return model.TravelMemoryResult{}, err
	}
	if analysis != nil {
		sourceRuns = slices.Clone(sourceRuns)
		for _, row := range rows {
			if row.Origin == "inferred" {
				for _, id := range append(slices.Clone(row.SourceRuns), row.SourceRunID) {
					if id != "" && !slices.Contains(sourceRuns, id) {
						sourceRuns = append(sourceRuns, id)
					}
				}
			}
		}
	}
	// Trusted origins and evidence provenance participate in idempotency identity.
	origins := []string{}
	for _, u := range updates {
		origins = append(origins, u.Source)
	}
	raw, _ := json.Marshal(struct {
		Updates  []model.TravelMemoryUpdate
		Origins  []string
		Analysis *model.TravelPreferenceAnalysis
		Sources  []string
	}{updates, origins, analysis, sourceRuns})
	sum := sha256.Sum256(raw)
	commit.Execution = ref
	commit.OperationID = id
	commit.RequestHash = hex.EncodeToString(sum[:])
	commit.SourceRuns = slices.Clone(sourceRuns)
	return store.CommitTravelMemory(ctx, commit)
}
