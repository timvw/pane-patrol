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
     
     HOWEVER: permission/confirmation dialogs OVERRIDE active indicators.
     If a "Do you want to proceed?", Yes/No prompt, Allow/Reject choice,
     or numbered selection menu appears ANYWHERE on screen, the agent IS
     blocked — even if a Build timer is still counting. The Build timer
     does not pause for permission dialogs.
    
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

Your response MUST be a JSON object with ALL of these fields (no markdown
fences, no extra text). Do NOT skip any field:
{
  "agent": "<detected agent name or 'not_an_agent'>",
  "blocked": <true or false>,
  "reason": "<one-line summary>",
  "waiting_for": "<verbatim dialog/prompt text copied from terminal — see rules below>",
  "actions": [{"keys": "<tmux send-keys>", "label": "<description>", "risk": "<low|medium|high>"}],
  "recommended": <index of recommended action, starting from 0>,
  "reasoning": "<detailed step-by-step analysis>"
}

Field "waiting_for" — REQUIRED when blocked is true, DO NOT OMIT:
- Copy the VERBATIM dialog/prompt/question lines from the terminal content.
- Include what is being requested, the question text, and available choices.
- Compact extract of the dialog only, not the entire screen.
- Use \n for line breaks. Examples:
  "Permission required\nAccess external directory ~/foo/bar\nAllow once  Allow always  Reject"
  "Bash command\ngit show a5e61c1 -- save.sh\nDo you want to proceed?\n1. Yes  2. Yes, and don't ask again  3. No"
  "idle at prompt"
- When blocked is false: set to "".

When "blocked" is true, "actions" MUST contain at least one action. Order
actions from most likely helpful to least. The first action should usually
be the one that approves/continues the agent's work.

When "blocked" is false, set "actions" to an empty array [] and
"recommended" to 0.

If this is not an AI coding agent, set "agent" to "not_an_agent", "blocked"
to false, "actions" to [], and explain what you see instead.

Terminal content:
