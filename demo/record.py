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
"""

import asyncio
import json
import os
import sys
import urllib.request

SHELLWRIGHT_URL = os.environ.get("SHELLWRIGHT_URL", "http://localhost:7498")
OUTPUT_DIR = os.environ.get("SHELLWRIGHT_OUTPUT", "./demo/output")

# ANSI colors for logging
CYAN = "\033[36m"
GREEN = "\033[32m"
DIM = "\033[2m"
RESET = "\033[0m"


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


async def demo_scan(session, output_dir: str):
    """Demo 1: CLI scan showing JSON output."""
    print(f"\n{'=' * 60}")
    print("Recording: pane-patrol scan")
    print(f"{'=' * 60}\n")

    # Start shell
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

    # Sanitize prompt to avoid leaking hostname/username in screenshots
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "export PS1='$ '\r", "delay_ms": 500},
    )

    # Start recording (low fps to keep GIF rendering fast)
    await call_tool(session, "shell_record_start", {"session_id": sid, "fps": 4})

    # Run pane-patrol scan with jq formatting
    # exclude_sessions in .pane-patrol.yaml handles AIGGTM filtering
    scan_cmd = (
        "clear && ./bin/pane-patrol scan 2>/dev/null"
        " | jq '[ .[] | {target, agent, blocked, reason} ]'"
    )
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": f"{scan_cmd}\r", "delay_ms": 500},
    )
    # Wait for LLM to evaluate all panes (~2-3s each, ~10 panes)
    await wait(60)

    # Check what's on screen
    await read_buffer(session, sid)

    # Screenshot
    data = await call_tool(
        session,
        "shell_screenshot",
        {
            "session_id": sid,
            "name": "scan-output",
        },
    )
    await download(data, output_dir)
    await wait(2)

    # Stop recording
    data = await call_tool(
        session,
        "shell_record_stop",
        {
            "session_id": sid,
            "name": "demo-scan",
        },
    )
    await download(data, output_dir)

    # Stop shell
    await call_tool(session, "shell_stop", {"session_id": sid})


async def demo_supervisor(session, output_dir: str):
    """Demo 2: Supervisor TUI with display filter cycling."""
    print(f"\n{'=' * 60}")
    print("Recording: pane-patrol supervisor")
    print(f"{'=' * 60}\n")

    # Start shell
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

    # Start recording
    await call_tool(session, "shell_record_start", {"session_id": sid, "fps": 10})
    await wait(1)

    # Clear and launch supervisor — use large delay_ms to wait for LLM scan
    # inside the MCP call (keeps connection alive vs asyncio.sleep)
    await call_tool(
        session,
        "shell_send",
        {
            "session_id": sid,
            "input": "clear && ./bin/pane-patrol supervisor\r",
            "delay_ms": 20000,
        },
    )

    # Screenshot: default view (blocked filter)
    data = await call_tool(
        session,
        "shell_screenshot",
        {"session_id": sid, "name": "supervisor-blocked"},
    )
    await download(data, output_dir)

    # Cycle filters: f -> agents, f -> all, f -> blocked, then right arrow for actions
    for key, name in [
        ("f", "supervisor-agents"),
        ("f", "supervisor-all"),
        ("f", None),
        ("\x1b[C", "supervisor-actions"),
    ]:
        await call_tool(
            session,
            "shell_send",
            {"session_id": sid, "input": key, "delay_ms": 1500},
        )
        if name:
            data = await call_tool(
                session,
                "shell_screenshot",
                {"session_id": sid, "name": name},
            )
            await download(data, output_dir)

    # Quit supervisor
    await call_tool(
        session,
        "shell_send",
        {"session_id": sid, "input": "q", "delay_ms": 1000},
    )

    # Stop recording
    data = await call_tool(
        session,
        "shell_record_stop",
        {
            "session_id": sid,
            "name": "demo-supervisor",
        },
    )
    await download(data, output_dir)

    # Stop shell
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
