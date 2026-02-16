Analyze this terminal screen capture from a terminal multiplexer pane.

Determine:

1. Whether this pane is running an AI coding agent (e.g., Claude Code,
   OpenCode, Codex, Cursor, Gemini CLI, AMP, Augment, Aider, or any
   similar AI coding assistant).
2. If it IS an agent, whether it is BLOCKED — meaning it is NOT actively
   working and is waiting for human input. An agent is blocked if ANY of
   these are true:
    - A confirmation/approval dialog (Confirm/Cancel, Yes/No, Y/n)
    - A permission prompt ("Permission required", file access, tool use,
      network access, "Allow once / Allow always / Reject")
    - The agent asked the user a question and is waiting for a typed response
      (look for a question mark in the agent's last output followed by an
      empty input area or blinking cursor)
    - An interactive selection menu awaiting a choice
    - An error state requiring human intervention (e.g., API errors, rate limits)
    - A cost/billing approval prompt
    - The agent finished its task and is idle at its input prompt (no active
      "Build", "Plan", or thinking indicator is spinning/running — the status
      bar shows a completed state and the input area is empty and waiting)
   
    An agent is NOT blocked only when it is actively working: you can see
    a running spinner, an active "Build" or "Plan" indicator with elapsed
    time still counting, tool calls being executed, or a subagent/task
    running in the background (e.g., "Explore Task", "Task", or
    "General Task" with a spinner or tool call count). A progress bar
    (e.g., "■■■⬝⬝⬝⬝⬝") or "esc interrupt" in the status bar also
    indicates active execution.
    
    Special case — stuck subagent: If a subagent/task shows a spinner but
    its toolcall count is "(0 toolcalls)" and there is NO "esc interrupt"
    or progress bar in the status bar, the subagent's API call has likely
    stalled. Classify as BLOCKED. Suggest sending directive text to the
    parent agent (e.g., "the task appears stuck, please continue without
    it") as a low-risk action — this causes the parent to cancel the
    stuck subagent and proceed on its own.

3. If it IS blocked, what actions could unblock it. For each action provide:
   - "keys": the exact tmux send-keys input. Use literal text for typed
     responses (e.g., "y", "yes", "continue") or tmux key names for
     control sequences (e.g., "C-c" for Ctrl+C, "Enter" for Enter key,
     "Escape" for Escape key, "Down" for arrow down).
   - "label": a short human-readable description of what the action does.
    - "risk": risk to the user's system, data, and security — NOT risk to
      the agent's progress. "low" (safe, conservative, denying access),
      "medium" (grants limited access, may change state), or "high"
      (grants broad access, destructive commands, irreversible changes).
      Rejecting/denying a permission request is always "low" risk.
      Granting persistent or broad access is "medium" or "high".

Think step by step about what you observe on screen.

Respond ONLY with a JSON object (no markdown fences, no extra text):
{
  "agent": "<detected agent name or 'not_an_agent'>",
  "blocked": <true or false>,
  "reason": "<one-line summary>",
  "actions": [
    { "keys": "<tmux send-keys input>", "label": "<description>", "risk": "<low|medium|high>" }
  ],
  "recommended": <index of recommended action, starting from 0>,
  "reasoning": "<detailed step-by-step analysis of what you see on screen>"
}

When "blocked" is true, "actions" MUST contain at least one action. Order
actions from most likely helpful to least. The first action should usually
be the one that approves/continues the agent's work.

When "blocked" is false, set "actions" to an empty array [] and
"recommended" to 0.

If this is not an AI coding agent, set "agent" to "not_an_agent", "blocked"
to false, "actions" to [], and explain what you see instead.

Terminal content:
