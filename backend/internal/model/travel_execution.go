package model

type TravelDecision struct {
	RunID        string            `json:"run_id"`
	DecisionKey  string            `json:"decision_key"`
	Stage        string            `json:"stage"`
	RequestHash  string            `json:"request_hash"`
	Response     Message           `json:"response"`
	OperationIDs map[string]string `json:"operation_ids"`
}
