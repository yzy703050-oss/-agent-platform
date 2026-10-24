package model

import "encoding/json"

// TravelToolResult is the durable native-result header used by the execution
// machine. Heterogeneous domain payloads stay in Data; Raw preserves the full
// model observation without importing Eino into business models.
type TravelToolResult struct {
	CallID         string           `json:"call_id"`
	Name           string           `json:"name"`
	Status         string           `json:"status"`
	Data           json.RawMessage  `json:"data,omitempty"`
	Operation      string           `json:"operation,omitempty"`
	TripPlanID     string           `json:"trip_plan_id,omitempty"`
	TripPlanIDs    []string         `json:"trip_plan_ids,omitempty"`
	Revision       int64            `json:"revision,omitempty"`
	Reason         string           `json:"reason,omitempty"`
	StopReason     string           `json:"stop_reason,omitempty"`
	StopPlanning   bool             `json:"stop_planning,omitempty"`
	RenderedAnswer string           `json:"rendered_answer,omitempty"`
	Validation     *TripValidation  `json:"validation,omitempty"`
	Error          *TravelRuleError `json:"error,omitempty"`
	Raw            json.RawMessage  `json:"-"`
}
