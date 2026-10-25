package travel

import _ "embed"

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
)

func roleInstruction(role string) string {
	switch role {
	case "verifier":
		return reviewInstruction
	case "summary":
		return summaryInstruction
	default:
		return instruction
	}
}
