package travel

import (
	_ "embed"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Original evaluated Python strings, frozen by source-baseline.json. Do not
// shorten these or change their tool vocabulary to fit platform DTOs.
var (
	//go:embed prompts/main.txt
	instruction string
	//go:embed prompts/review.txt
	reviewInstruction string
	//go:embed prompts/summary.txt
	summaryInstruction string
	//go:embed prompts/reply.txt
	replyPolicy string
	//go:embed prompts/rail.txt
	railPlanningPolicy string
	// User-authorized additions are versioned separately from the source baseline.
	//go:embed prompts/correction.txt
	correctionInstruction string
	//go:embed prompts/no_followup.txt
	interactionInstruction string
	//go:embed prompts/planning_summary.txt
	planningSummaryInstruction string
)

var (
	//go:embed prompts/runtime/main.txt
	runtimeMain string
	//go:embed prompts/runtime/planning_summary.txt
	runtimePlanningSummary string
	//go:embed prompts/runtime/verifier.txt
	runtimeVerifier string
	//go:embed prompts/runtime/planning.txt
	runtimePlanning string
	//go:embed prompts/runtime/review.txt
	runtimeReview string
	//go:embed prompts/runtime/stopped.txt
	runtimeStopped string
)

type promptState struct {
	Role                                           string
	Stopped, ProtocolRecovery, InteractionRecovery bool
}

func composePromptPrefix(state promptState) []*schema.Message {
	role, text := state.Role, runtimeMain
	switch role {
	case "planning_summary":
		text = runtimePlanningSummary
	case "verifier":
		text = runtimeVerifier
	case "summary":
		text = summaryInstruction
	default:
		role = "main"
	}
	if state.Stopped {
		role, text = "stopped", runtimeStopped
	}
	layer := func(name, content string) *schema.Message {
		return schema.SystemMessage("[travel-prompt:" + name + "]\n" + content)
	}
	out := []*schema.Message{layer("role:"+role, text), layer("reply", replyPolicy)}
	if role == "main" || role == "planning_summary" || role == "verifier" {
		out = append(out, layer("planning", runtimePlanning))
	}
	if role == "planning_summary" || role == "verifier" {
		out = append(out, layer("review", runtimeReview))
	}
	if state.ProtocolRecovery {
		out = append(out, layer("recovery:protocol", regenerationInstruction))
	}
	if state.InteractionRecovery {
		out = append(out, layer("recovery:interaction", interactionRegenerationInstruction))
	}
	return out
}

// Replace only managed rules: system data, summaries and complete tool pairs
// remain untouched when a generation changes roles or is retried.
func replacePromptPrefix(messages []*schema.Message, state promptState) []*schema.Message {
	out := composePromptPrefix(state)
	for _, msg := range messages {
		if msg.Role == schema.System && strings.HasPrefix(msg.Content, "[travel-prompt:") {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func roleInstruction(role string) string {
	return composePromptPrefix(promptState{Role: role})[0].Content
}

func promptStateFromMessages(messages []*schema.Message) promptState {
	state := promptState{Role: "main"}
	for _, msg := range messages {
		if msg.Role != schema.System {
			continue
		}
		if strings.HasPrefix(msg.Content, "[travel-prompt:role:") {
			role, _, _ := strings.Cut(strings.TrimPrefix(msg.Content, "[travel-prompt:role:"), "]")
			state.Role = role
			state.Stopped = role == "stopped"
		}
		state.ProtocolRecovery = state.ProtocolRecovery || strings.HasPrefix(msg.Content, "[travel-prompt:recovery:protocol]")
		state.InteractionRecovery = state.InteractionRecovery || strings.HasPrefix(msg.Content, "[travel-prompt:recovery:interaction]")
	}
	return state
}

func runtimePromptMaterial() string {
	return strings.Join([]string{"prompt-layers-v1", runtimeMain, runtimePlanningSummary, runtimeVerifier, runtimePlanning, runtimeReview, runtimeStopped, summaryInstruction, replyPolicy, regenerationInstruction, interactionRegenerationInstruction}, "\x00")
}
