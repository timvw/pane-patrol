#!/usr/bin/env bash
set -euo pipefail

mkdir -p "$HOME" "$XDG_RUNTIME_DIR"

go build -o /tmp/pane-patrol ./

tmux new-session -d -s e2e -n one "sleep 120"
tmux new-window -t e2e -n two "sleep 120"

socket_path="$XDG_RUNTIME_DIR/pane-patrol/events.sock"

tmux new-session -d -s supervisor "/tmp/pane-patrol supervisor --no-embed --hook-first --event-socket '$socket_path' --theme light"

cleanup() {
	tmux kill-server >/dev/null 2>&1 || true
}
trap cleanup EXIT

for _ in $(seq 1 80); do
	if [[ -S "$socket_path" ]]; then
		break
	fi
	sleep 0.1
done

python3 - <<'PY'
import json
import os
import socket
import time

sock = os.environ["XDG_RUNTIME_DIR"] + "/pane-patrol/events.sock"
payload = {
    "assistant": "claude",
    "state": "waiting_input",
    "target": "e2e:0.0",
    "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "message": "approval needed",
}
s = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
s.sendto(json.dumps(payload).encode("utf-8"), sock)
s.close()
PY

sleep 1

if ! tmux ls | grep -q '^supervisor:'; then
	echo "supervisor session not running"
	exit 1
fi

echo "integration hook smoke passed"
