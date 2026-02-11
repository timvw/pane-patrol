Analyze this terminal screen capture from a terminal multiplexer pane.

Determine:

1. Whether this pane is running an AI coding agent (e.g., Claude Code,
   OpenCode, Codex, Cursor, Gemini CLI, AMP, Augment, Aider, or any
   similar AI coding assistant).
2. If it IS an agent, whether it is BLOCKED — meaning it is waiting for
   human input such as:
   - A confirmation/approval dialog (Confirm/Cancel, Yes/No, Y/n)
   - A permission prompt (file access, tool use, network access)
   - A question requiring a typed response
   - An interactive selection menu awaiting a choice
   - An error state requiring human intervention
   - A cost/billing approval prompt

Think step by step about what you observe on screen.

Respond ONLY with a JSON object (no markdown fences, no extra text):
{
  "agent": "<detected agent name or 'not_an_agent'>",
  "blocked": <true or false>,
  "reason": "<one-line summary>",
  "reasoning": "<detailed step-by-step analysis of what you see on screen>"
}

If this is not an AI coding agent, set "agent" to "not_an_agent", "blocked"
to false, and explain what you see instead.

Terminal content:
