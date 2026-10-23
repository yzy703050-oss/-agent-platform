package travel

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cloudwego/eino/adk"
)

type Phase string

const (
	PhasePlanning   Phase = "planning"
	PhaseQuerying   Phase = "querying"
	PhaseValidating Phase = "validating"
	PhaseReply      Phase = "reply"
	stateKey              = "platform.travel.state.v1"
)

// State is serialized in the Eino session, independently of live Go objects.
type State struct {
	Version    int   `json:"version"`
	Phase      Phase `json:"phase"`
	ModelCalls int   `json:"model_calls"`
}

func readState(ctx context.Context) (State, error) {
	v, ok := adk.GetSessionValue(ctx, stateKey)
	if !ok {
		return State{Version: 1, Phase: PhasePlanning}, nil
	}
	text, ok := v.(string)
	if !ok {
		return State{}, errors.New("invalid travel state encoding")
	}
	var s State
	if json.Unmarshal([]byte(text), &s) != nil || s.Version != 1 || s.ModelCalls < 0 {
		return State{}, errors.New("incompatible travel state")
	}
	switch s.Phase {
	case PhasePlanning, PhaseQuerying, PhaseValidating, PhaseReply:
	default:
		return State{}, errors.New("invalid travel phase")
	}
	return s, nil
}
func saveState(ctx context.Context, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	adk.AddSessionValue(ctx, stateKey, string(b))
	return nil
}
