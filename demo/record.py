#!/usr/bin/env python3
"""
pane-patrol demo recorder — drives shellwright MCP to create GIF recordings.

Usage:
    # Start shellwright in HTTP mode first:
    npx -y @dwmkerr/shellwright --http --font-size 16 --cols 140 --rows 35

    # Then run this script:
    uv run --with "mcp[cli]" python demo/record.py

    # Or record a specific demo:
    uv run --with "mcp[cli]" python demo/record.py scan
    uv run --with "mcp[cli]" python demo/record.py supervisor

Environment variables:
    SHELLWRIGHT_URL  — shellwright endpoint (default: http://localhost:7498)
    SHELLWRIGHT_OUTPUT — output directory (default: ./demo/output)
    DEMO_HOST — SSH host alias for remote machine (default: aspire)
    PANE_PATROL — path to pane-patrol binary on remote (default: ~/bin/pane-patrol)
"""

import asyncio
import json
import os
import sys
import urllib.request

SHELLWRIGHT_URL = os.environ.get("SHELLWRIGHT_URL", "http://localhost:7498")
OUTPUT_DIR = os.environ.get("SHELLWRIGHT_OUTPUT", "./demo/output")
DEMO_HOST = os.environ.get("DEMO_HOST", "aspire")
PANE_PATROL = os.environ.get("PANE_PATROL", "~/bin/pane-patrol")

# ANSI colors for logging
CYAN = "\033[36m"
GREEN = "\033[32m"
DIM = "\033[2m"
RESET = "\033[0m"

# Key escape sequences
UP = "\x1b[A"
DOWN = "\x1b[B"
ENTER = "\r"
CTRL_B = "\x02"


async def call_tool(session, name: str, args: dict) -> dict:
    """Call a shellwright MCP tool and return parsed JSON response."""
    print(
        f"  {CYAN}{name}{RESET}({', '.join(f'{k}={v!r}' for k, v in args.items() if k != 'session_id')})"
    )
    result = await session.call_tool(name, args)
    text = ""
    if result.content:
        for content in result.content:
            if hasattr(content, "text"):
                text += content.text
    try:
        return json.loads(text)
    except (json.JSONDecodeError, TypeError):
        return {"raw": text}


async def download(data: dict, output_dir: str):
    """Download file if response contains download_url."""
    if "download_url" in data and "filename" in data:
        path = os.path.join(output_dir, data["filename"])
        urllib.request.urlretrieve(data["download_url"], path)
        print(f"  {GREEN}saved:{RESET} {path}")


async def wait(seconds: float):
    """Wait with a message."""
    print(f"  {DIM}waiting {seconds}s...{RESET}")
    await asyncio.sleep(seconds)


async def read_buffer(session, sid: str) -> str:
    """Read the terminal buffer and print a preview."""
    data = await call_tool(session, "shell_read", {"session_id": sid})
    text = data.get("raw", data.get("content", ""))
    # Show first 3 non-empty lines as preview
    lines = [l for l in text.strip().split("\n") if l.strip()][:3]
    if lines:
        for l in lines:
            print(f"    {DIM}| {l[:100]}{RESET}")
    return text


async def start_shell(session) -> tuple[str, dict]:
    """Start a new shellwright shell session with sanitized prompt."""
    shell = await call_tool(
        session,
        "shell_start",
        {
            "command": "bash",
            "args": ["--login", "-i"],
            "cols": 140,
            "rows": 35,
            "theme": "one-dark",
        },
    )
    sid = shell["shell_session_id"]
    # Sanitize the LOCAL prompt immediately to avoid leaking hostname/username
    # in any recorded frame (even briefly before clear).
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "export PS1='$ '\r", "delay_ms": 500},
    )
    return sid, shell


async def setup_ssh(session, sid: str):
    """SSH to remote host and sanitize prompt. Done BEFORE recording starts."""
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": f"ssh {DEMO_HOST}\r", "delay_ms": 3000},
    )
    # Sanitize prompt on remote to avoid leaking hostname/username
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "export PS1='$ '\r", "delay_ms": 500},
    )
    # Clear screen so recording starts clean (no SSH banner/MOTD visible)
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "clear\r", "delay_ms": 500},
    )


async def demo_scan(session, output_dir: str):
    """Demo 1: CLI scan showing JSON output with agent detection."""
    print(f"\n{'=' * 60}")
    print("Recording: pane-patrol scan")
    print(f"{'=' * 60}\n")

    sid, _ = await start_shell(session)

    # SSH and sanitize (not recorded)
    await setup_ssh(session, sid)

    # Start recording
    await call_tool(session, "shell_record_start", {"session_id": sid, "fps": 4})

    # Run pane-patrol scan with jq formatting — filter to detected agents only
    scan_cmd = (
        f"clear && {PANE_PATROL} scan 2>/dev/null"
        " | jq '[ .[] | select(.agent != \"unknown\") | {target, agent, blocked, reason, waiting_for} ]'"
    )
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": f"{scan_cmd}\r", "delay_ms": 500},
    )
    # Deterministic parsers are fast (<100ms total), but allow time for
    # pane capture + SSH round-trip + jq rendering
    await wait(8)

    # Check what's on screen
    await read_buffer(session, sid)

    # Screenshot
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "scan-output"},
    )
    await download(data, output_dir)
    await wait(2)

    # Stop recording
    data = await call_tool(
        session,
        "shell_record_stop",
        {"session_id": sid, "name": "demo-scan"},
    )
    await download(data, output_dir)

    # Cleanup: exit SSH, stop shell
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "exit\r", "delay_ms": 500},
    )
    await call_tool(session, "shell_stop", {"session_id": sid})


async def demo_supervisor(session, output_dir: str):
    """Demo 2: Supervisor TUI with filter cycling and jump-to-pane."""
    print(f"\n{'=' * 60}")
    print("Recording: pane-patrol supervisor")
    print(f"{'=' * 60}\n")

    sid, _ = await start_shell(session)

    # SSH and sanitize (not recorded)
    await setup_ssh(session, sid)

    # Create a tmux session for the demo so jump-to-pane works.
    # The supervisor needs to run inside tmux to switch clients on Enter.
    # Kill any leftover demo session first, then create fresh.
    await call_tool(
        session,
        "shell_send",
        {
            "session_id": sid,
            "input": "tmux kill-session -t demo 2>/dev/null; tmux new-session -s demo\r",
            "delay_ms": 1500,
        },
    )
    # Re-sanitize prompt inside tmux (new shell) and disable status bar
    await call_tool(
        session,
        "shell_send",
        {
            "session_id": sid,
            "input": "export PS1='$ ' && tmux set-option status off\r",
            "delay_ms": 500,
        },
    )
    # Clear screen inside tmux so recording starts clean
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "clear\r", "delay_ms": 500},
    )

    # Start recording
    await call_tool(session, "shell_record_start", {"session_id": sid, "fps": 10})
    await wait(1)

    # Launch supervisor (default command)
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": f"clear && {PANE_PATROL}\r", "delay_ms": 8000},
    )

    # Screenshot: default view (blocked filter)
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-blocked"},
    )
    await download(data, output_dir)

    # --- Filter cycling with 'f' ---

    # f -> agents filter
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "f", "delay_ms": 1500},
    )
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-agents"},
    )
    await download(data, output_dir)

    # f -> all filter
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "f", "delay_ms": 1500},
    )
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-all"},
    )
    await download(data, output_dir)

    # f -> back to blocked
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "f", "delay_ms": 1500},
    )

    # --- Navigate to a pane and jump ---
    #
    # After pressing 'f', the cursor resets to position 0 then
    # clampCursorToPane() moves it to the first pane (hal-9000:0.0).
    # Session headers are auto-skipped during navigation, so one
    # Down press jumps from hal-9000:0.0 directly to skynet:0.0.
    #
    # We target skynet because it has the most interesting state
    # (question dialog with options) and avoids showing the Claude
    # welcome banner which contains an email address.

    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "j", "delay_ms": 500},
    )

    # Brief pause to show the selected pane's details in the right panel
    await wait(1)

    # Screenshot: pane selected, showing actions panel
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-selected"},
    )
    await download(data, output_dir)

    # Press Enter to jump to the pane
    # The supervisor exits and tmux switches to the target pane,
    # showing the actual agent TUI (OpenCode with question dialog).
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": ENTER, "delay_ms": 2000},
    )

    # Screenshot: now showing the OpenCode TUI directly
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-jump"},
    )
    await download(data, output_dir)

    await wait(1)

    # Stop recording
    data = await call_tool(
        session,
        "shell_record_stop",
        {"session_id": sid, "name": "demo-supervisor"},
    )
    await download(data, output_dir)

    # Cleanup: detach from tmux, kill demo session, exit SSH
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": f"{CTRL_B}d", "delay_ms": 1000},
    )
    await call_tool(
        session,
        "shell_send",
        {
            "session_id": sid,
            "input": "tmux kill-session -t demo 2>/dev/null\r",
            "delay_ms": 500,
        },
    )
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "exit\r", "delay_ms": 500},
    )
    await call_tool(session, "shell_stop", {"session_id": sid})


async def main():
    from mcp import ClientSession
    from mcp.client.streamable_http import streamablehttp_client

    os.makedirs(OUTPUT_DIR, exist_ok=True)

    demos = {
        "scan": demo_scan,
        "supervisor": demo_supervisor,
    }

    # Select which demos to run
    requested = sys.argv[1:] if len(sys.argv) > 1 else list(demos.keys())
    for name in requested:
        if name not in demos:
            print(f"Unknown demo: {name}. Available: {', '.join(demos.keys())}")
            sys.exit(1)

    print(f"{DIM}shellwright:{RESET} {SHELLWRIGHT_URL}")
    print(f"{DIM}output:{RESET} {OUTPUT_DIR}")
    print(f"{DIM}host:{RESET} {DEMO_HOST}")
    print(f"{DIM}demos:{RESET} {', '.join(requested)}")

    try:
        async with streamablehttp_client(f"{SHELLWRIGHT_URL}/mcp") as (read, write, _):
            async with ClientSession(read, write) as session:
                await session.initialize()
                print(f"{GREEN}Connected to shellwright{RESET}")

                for name in requested:
                    await demos[name](session, OUTPUT_DIR)

        print(f"\n{GREEN}All demos recorded. Output in {OUTPUT_DIR}/{RESET}")

    except Exception as e:
        if "Connect" in type(e).__name__ or "connection" in str(e).lower():
            print(
                f"\nError: Cannot connect to shellwright at {SHELLWRIGHT_URL}\n"
                f"Start it first: npx -y @dwmkerr/shellwright --http --font-size 16 --cols 140 --rows 35",
                file=sys.stderr,
            )
        else:
            raise
        sys.exit(1)


if __name__ == "__main__":
    asyncio.run(main())
