#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOME_DIR="${HOME}"

install_one() {
	local src="$1"
	local dest="$2"
	mkdir -p "$(dirname "$dest")"
	cp "$src" "$dest"
	chmod 0755 "$dest"
}

install_one "$ROOT_DIR/hooks/claude/emit.sh" "$HOME_DIR/.claude/hooks/pane-patrol-emit.sh"
install_one "$ROOT_DIR/hooks/opencode/emit.sh" "$HOME_DIR/.config/opencode/hooks/pane-patrol-emit.sh"
install_one "$ROOT_DIR/hooks/codex/emit.sh" "$HOME_DIR/.config/codex/hooks/pane-patrol-emit.sh"

printf "Installed hooks:\n"
printf "%s\n" "- $HOME_DIR/.claude/hooks/pane-patrol-emit.sh"
printf "%s\n" "- $HOME_DIR/.config/opencode/hooks/pane-patrol-emit.sh"
printf "%s\n" "- $HOME_DIR/.config/codex/hooks/pane-patrol-emit.sh"
