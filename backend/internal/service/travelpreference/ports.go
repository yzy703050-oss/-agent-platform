package travelpreference

import (
	"agent-platform/backend/internal/model"
	"context"
)

type Store interface {
	CommitTravelPreference(context.Context, model.TravelPreferenceCommit) (model.TravelPreference, error)
	ReadTravelPreferences(context.Context, model.Scope, string) ([]model.TravelPreference, error)
}
