package evaluator

import "testing"

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain JSON unchanged",
			input: `{"agent": true, "blocked": false}`,
			want:  `{"agent": true, "blocked": false}`,
		},
		{
			name:  "fenced json block",
			input: "```json\n{\"agent\": true}\n```",
			want:  `{"agent": true}`,
		},
		{
			name:  "fenced without language",
			input: "```\n{\"agent\": true}\n```",
			want:  `{"agent": true}`,
		},
		{
			name:  "fenced with whitespace",
			input: "  ```json\n{\"key\": \"value\"}\n```  ",
			want:  `{"key": "value"}`,
		},
		{
			name:  "multiline JSON in fences",
			input: "```json\n{\n  \"agent\": true,\n  \"blocked\": false\n}\n```",
			want:  "{\n  \"agent\": true,\n  \"blocked\": false\n}",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only fences no content",
			input: "```json\n```",
			want:  "",
		},
		{
			name:  "triple backticks inside content preserved",
			input: `{"code": "use backticks"}`,
			want:  `{"code": "use backticks"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownFences(%q) =\n  %q\nwant:\n  %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPromptsLoaded(t *testing.T) {
	// Verify that embedded prompts are non-empty
	if SystemPrompt == "" {
		t.Error("SystemPrompt is empty — embed directive may have failed")
	}
	if UserPromptTemplate == "" {
		t.Error("UserPromptTemplate is empty — embed directive may have failed")
	}
}
