#!/usr/bin/env bash
set -u

assistant="opencode"
state="${1:-waiting_input}"
message="${2:-}"

if [[ -z "${TMUX_PANE:-}" ]]; then
	exit 0
fi

target="$(tmux display-message -t "$TMUX_PANE" -p "#{session_name}:#{window_index}.#{pane_index}" 2>/dev/null || true)"
if [[ -z "$target" ]]; then
	exit 0
fi

socket="${PANE_PATROL_EVENT_SOCKET:-}"
if [[ -z "$socket" ]]; then
	if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
		socket="${XDG_RUNTIME_DIR}/pane-patrol/events.sock"
	else
		socket="/tmp/pane-patrol-$(id -u)/events.sock"
	fi
fi

ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

python3 - "$socket" "$assistant" "$state" "$target" "$message" "$ts" <<'PY' >/dev/null 2>&1 || true
import json
import socket
import sys

sock_path, assistant, state, target, message, ts = sys.argv[1:7]
payload = json.dumps({
    "assistant": assistant,
    "state": state,
    "target": target,
    "message": message,
    "ts": ts,
}, separators=(",", ":")).encode("utf-8")

s = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
s.setblocking(False)
try:
    s.sendto(payload, sock_path)
except OSError:
    pass
finally:
    s.close()
PY

exit 0
