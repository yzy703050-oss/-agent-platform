package contracts

import "encoding/json"

type Event struct {
	RunID   string          `json:"run_id"`
	Type    string          `json:"type"`
	Attempt int64           `json:"attempt"`
	Seq     int64           `json:"seq"`
	Payload json.RawMessage `json:"payload"`
}
