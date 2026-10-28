package travel

import (
	"strings"
	"testing"

	traveltools "agent-platform/backend/internal/agenttools/travel"
)

func TestTravelPromptFingerprint(t *testing.T) {
	prompts, tools := runtimePromptMaterial(), traveltools.RuntimeToolInstructionMaterial()
	for _, text := range []string{"prompt-layers-v2", runtimeMain, runtimePlanningSummary, runtimePlanningRecovery, runtimeVerifier, runtimePlanning, runtimeReview, runtimeStopped, summaryInstruction, replyPolicy, regenerationInstruction, interactionRegenerationInstruction} {
		if text == "" || !strings.Contains(prompts, text) {
			t.Fatal("effective prompt/recovery resource missing from fingerprint material")
		}
	}
	if !strings.Contains(tools, "conditions.arrival_before") || !strings.Contains(tools, "POLICY_OBSERVATION_REQUIRED") {
		t.Fatal("runtime tool help not fingerprinted")
	}
	original := travelFingerprint("identity", "contracts", prompts, tools)
	for _, text := range []string{"prompt-layers-v2", runtimeMain, runtimePlanningSummary, runtimePlanningRecovery, runtimeVerifier, runtimePlanning, runtimeReview, runtimeStopped, summaryInstruction, replyPolicy, regenerationInstruction, interactionRegenerationInstruction} {
		if travelFingerprint("identity", "contracts", strings.Replace(prompts, text, text+"changed", 1), tools) == original {
			t.Fatal("prompt/resource version change kept old compatibility fingerprint")
		}
	}
	if travelFingerprint("identity", "contracts", prompts, tools+"changed") == original {
		t.Fatal("tool instructions change kept old compatibility fingerprint")
	}
}
