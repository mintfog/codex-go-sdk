package codex

import "strings"

func normalizeReasoningEffortForModel(model, effort string) string {
	trimmedModel := strings.TrimSpace(model)
	trimmedEffort := strings.TrimSpace(effort)
	// GPT-5.3-Codex requires the legacy xhigh fallback for max and ultra.
	if trimmedModel == "gpt-5.3-codex" && (trimmedEffort == "max" || trimmedEffort == "ultra") {
		return "xhigh"
	}
	return trimmedEffort
}
