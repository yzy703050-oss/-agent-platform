package contracts

import "encoding/json"

type Event struct {
	RunID, Type  string
	Attempt, Seq int64
	Payload      json.RawMessage
}
