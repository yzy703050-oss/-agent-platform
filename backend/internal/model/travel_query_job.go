package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

type TravelQueryJob struct {
	ID           string          `json:"id"`
	ParentCallID string          `json:"parent_call_id"`
	Query        TravelQuery     `json:"query"`
	Result       *TravelEvidence `json:"result,omitempty"`
}

func TravelQueryJobID(operationID string, index int, q TravelQuery) string {
	raw, _ := json.Marshal([]string{operationID, strconv.Itoa(index), q.TaskID, q.Domain})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
