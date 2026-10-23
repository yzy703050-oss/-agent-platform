package contracts

import (
	"bytes"
	"encoding/json"
	"io"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
)

type TravelPhase struct {
	Phase  string `json:"phase"`
	PlanID string `json:"plan_id,omitempty"`
}

func ValidateTravelPhase(data []byte) error {
	if len(data) > 1024 {
		return model.ErrInvalidInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var p TravelPhase
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return model.ErrInvalidInput
	}
	switch p.Phase {
	case "planning", "querying", "validating", "reply":
	default:
		return model.ErrInvalidInput
	}
	if p.PlanID != "" {
		if _, err := uuid.Parse(p.PlanID); err != nil {
			return model.ErrInvalidInput
		}
	}
	return nil
}
