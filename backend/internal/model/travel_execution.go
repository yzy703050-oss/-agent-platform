package model

import "encoding/json"

type TravelDecision struct {
	InputMessages []Message         `json:"input_messages,omitempty"`
	FlowSnapshot  json.RawMessage   `json:"flow_snapshot,omitempty"`
	RunID         string            `json:"run_id"`
	DecisionKey   string            `json:"decision_key"`
	Stage         string            `json:"stage"`
	RequestHash   string            `json:"request_hash"`
	Response      Message           `json:"response"`
	OperationIDs  map[string]string `json:"operation_ids"`
}
