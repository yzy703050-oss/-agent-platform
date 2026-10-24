package trip

import (
	"agent-platform/backend/internal/model"
	"slices"
)

func validateConstraints(c model.TripConstraints) error {
	if c.MaxBudgetFen != nil && *c.MaxBudgetFen < 0 {
		return invalid("invalid monetary constraint")
	}
	if c.BudgetStrength != "" && c.BudgetStrength != model.Hard && c.BudgetStrength != model.Soft {
		return invalid("invalid budget strength")
	}
	for key, strength := range c.Strengths {
		if key != "train_kinds" && key != "seat_classes" {
			return invalid("unknown constraint")
		}
		if strength != model.Hard && strength != model.Soft {
			return invalid("invalid constraint strength")
		}
	}
	return nil
}

func matchesTrain(candidate model.TravelCandidate, c model.TripConstraints) bool {
	if len(c.TrainKinds) > 0 && c.Strengths["train_kinds"] != model.Soft && !slices.ContainsFunc(c.TrainKinds, func(kind string) bool { return canonicalTrainKind(kind) == canonicalTrainKind(candidate.TrainKind) }) {
		return false
	}
	if len(c.SeatClasses) > 0 && c.Strengths["seat_classes"] != model.Soft && !slices.Contains(c.SeatClasses, candidate.SeatClass) {
		return false
	}
	return true
}

// Older Go snapshots used the supplier's letter. New native contracts use the
// source enum. Never reinterpret an unknown value as a supported rail kind.
func canonicalTrainKind(kind string) string {
	switch kind {
	case "G", "high_speed":
		return "high_speed"
	case "D", "emu":
		return "emu"
	case "Z", "T", "K", "other":
		return "other"
	default:
		return kind
	}
}
func protectConstraints(old, next model.TripConstraints) error {
	if err := validateConstraints(next); err != nil {
		return err
	}
	if old.MaxBudgetFen != nil && old.BudgetStrength != model.Soft {
		if next.MaxBudgetFen == nil || next.BudgetStrength == model.Soft || *next.MaxBudgetFen > *old.MaxBudgetFen {
			return invalid("cannot weaken a hard budget")
		}
	}
	for key, oldValues := range map[string][]string{"train_kinds": old.TrainKinds, "seat_classes": old.SeatClasses} {
		if len(oldValues) == 0 || old.Strengths[key] == model.Soft {
			continue
		}
		if next.Strengths[key] == model.Soft {
			return invalid("cannot soften a hard constraint")
		}
		values := next.TrainKinds
		if key == "seat_classes" {
			values = next.SeatClasses
		}
		if len(values) == 0 {
			return invalid("cannot remove a hard constraint")
		}
		for _, v := range values {
			if !slices.Contains(oldValues, v) {
				return invalid("cannot widen a hard constraint")
			}
		}
	}
	return nil
}
