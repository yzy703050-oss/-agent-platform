package toolquery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"agent-platform/backend/internal/runtimeprotocol"
)

var ErrExpired = errors.New("saved evidence is outside its reuse window")
var ErrUnavailable = errors.New("query capability unavailable")

type QueryInput struct {
	CallKey, Domain, Mode, Provider string
	Arguments                       json.RawMessage
	SourceRefs                      []runtimev1.SourceRef
	ForceRefresh                    bool
}
type Result struct {
	Snapshot  map[string]json.RawMessage
	Coverage  string
	FetchedAt time.Time
	ExpiresAt *time.Time
}
type Provider struct {
	Domain, Mode, Name string
	ResourceKeys       []string
	Validate           func(json.RawMessage) error
	Call               func(context.Context, json.RawMessage) (Result, error)
}
type QueryState struct {
	Record     runtimev1.SharedToolCallRecord
	DeadlineAt time.Time
	Execution  runtimeports.Execution
}
type Store interface {
	runtimeports.AttemptStore
	BeginQuery(context.Context, runtimev1.ExecRef, QueryInput) (QueryState, error)
	FindSaved(context.Context, runtimev1.ExecRef, string) (runtimev1.SharedToolEvidenceRecord, bool, error)
	SaveQuery(context.Context, runtimev1.ExecRef, string, string, Result) (runtimev1.SharedToolEvidenceRecord, error)
	FailQuery(context.Context, runtimev1.ExecRef, string, *runtimev1.ErrorDetail) error
}

func ValidateObject(raw json.RawMessage) error {
	canonical, err := runtimeprotocol.CanonicalJSON(raw)
	if err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(canonical, &obj) != nil || obj == nil {
		return model.ErrInvalidInput
	}
	return nil
}
func Fingerprint(in QueryInput) (string, error) {
	if err := ValidateObject(in.Arguments); err != nil {
		return "", err
	}
	if in.SourceRefs == nil {
		in.SourceRefs = []runtimev1.SourceRef{}
	}
	b, err := json.Marshal(struct {
		Domain, Mode, Provider string
		Arguments              json.RawMessage
		SourceRefs             []runtimev1.SourceRef
		ForceRefresh           bool
	}{in.Domain, in.Mode, in.Provider, in.Arguments, in.SourceRefs, in.ForceRefresh})
	if err != nil {
		return "", err
	}
	b, err = runtimeprotocol.CanonicalJSON(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
