package evaluator

import (
	_ "embed"
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
