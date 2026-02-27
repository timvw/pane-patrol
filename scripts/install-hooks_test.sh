#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_HOME="$(mktemp -d)"
trap 'rm -rf "$TMP_HOME"' EXIT

HOME="$TMP_HOME" "$ROOT_DIR/scripts/install-hooks.sh" >/dev/null

test -x "$TMP_HOME/.claude/hooks/pane-patrol-emit.sh"
test -x "$TMP_HOME/.config/opencode/hooks/pane-patrol-emit.sh"
test -x "$TMP_HOME/.config/codex/hooks/pane-patrol-emit.sh"

echo "install-hooks test passed"
