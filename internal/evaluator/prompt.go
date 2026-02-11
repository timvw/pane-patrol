package evaluator

import (
	_ "embed"
	"strings"
)

// SystemPrompt is the system-level instruction for the LLM evaluator.
// Loaded from prompts/system.md at compile time.
//
//go:embed prompts/system.md
var SystemPrompt string

// UserPromptTemplate is the user-level prompt template.
// The pane content is appended after this template at runtime.
// Loaded from prompts/user.md at compile time.
//
//go:embed prompts/user.md
var UserPromptTemplate string

// stripMarkdownFences removes markdown code fences from LLM responses.
// Some models wrap JSON responses in ```json ... ``` despite being told not to.
// This is pure transport/formatting — not content interpretation (ZFC compliant).
func stripMarkdownFences(text string) string {
	text = strings.TrimSpace(text)
	// Strip ```json or ``` prefix.
	if strings.HasPrefix(text, "```") {
		// Find end of first line.
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
	}
	// Strip trailing ```.
	if strings.HasSuffix(text, "```") {
		text = text[:len(text)-3]
	}
	return strings.TrimSpace(text)
}
