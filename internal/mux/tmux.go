package mux

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// Tmux implements the Multiplexer interface for tmux.
type Tmux struct{}

// NewTmux creates a new tmux multiplexer.
func NewTmux() *Tmux {
	return &Tmux{}
}

// Name returns "tmux".
func (t *Tmux) Name() string {
	return "tmux"
}

// ListPanes returns all tmux panes, optionally filtered by session name pattern.
func (t *Tmux) ListPanes(ctx context.Context, filter string) ([]model.Pane, error) {
	// Format: session_name:window_index.pane_index\tpane_pid\tcurrent_command
	format := "#{session_name}:#{window_index}.#{pane_index}\t#{pane_pid}\t#{pane_current_command}"
	out, err := t.run(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}

	var re *regexp.Regexp
	if filter != "" {
		re, err = regexp.Compile(filter)
		if err != nil {
			return nil, fmt.Errorf("invalid filter pattern %q: %w", filter, err)
		}
	}

	var panes []model.Pane
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}

		target := parts[0]
		pid, _ := strconv.Atoi(parts[1])
		command := parts[2]

		pane, err := parseTarget(target)
		if err != nil {
			continue
		}
		pane.PID = pid
		pane.Command = command
		pane.ProcessTree = getProcessTree(pid)

		// Apply session name filter if provided.
		if re != nil && !re.MatchString(pane.Session) {
			continue
		}

		panes = append(panes, pane)
	}

	return panes, nil
}

// CapturePane captures the visible content of a tmux pane.
// Uses -p (stdout) and -J (joined, unwraps lines).
func (t *Tmux) CapturePane(ctx context.Context, target string) (string, error) {
	out, err := t.run(ctx, "capture-pane", "-t", target, "-p", "-J")
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane -t %s: %w", target, err)
	}
	return out, nil
}

// run executes a tmux command and returns its stdout.
func (t *Tmux) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w: %s", err, string(exitErr.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// getProcessTree returns the command lines of all descendant processes of the given PID.
// Uses a single "ps -eo pid,ppid,args" call to snapshot all processes, then builds
// the tree in Go — O(1) subprocess invocations instead of O(N) per scan.
//
// Walks up to maxProcessTreeDepth levels deep to capture subprocesses spawned by
// agents (covers wrapper scripts, version manager shims, and agent binaries).
// The result is capped at maxProcessTreeEntries to keep the verdict compact
// by excluding LSP servers and other long-running child processes that don't help classification.
// Returns nil on any error — process info is best-effort, never fatal.
const maxProcessTreeDepth = 5
const maxProcessTreeEntries = 15

func getProcessTree(pid int) []string {
	if pid <= 0 {
		return nil
	}

	// Single subprocess call: snapshot all processes with their parent PID.
	// "pid=" and "ppid=" suppress the header; "args=" gives full command line.
	cmd := exec.Command("ps", "-eo", "pid=,ppid=,args=")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Build parent -> children map and pid -> args map.
	type proc struct {
		pid  int
		ppid int
		args string
	}
	children := map[int][]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "  PID  PPID ARGS..." — fields are space-separated,
		// but ARGS can contain spaces. Split into at most 3 fields.
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 3 {
			// Try harder: ps output may have variable whitespace between PID and PPID.
			fields = strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			// Rejoin everything after the second field as args.
			// Find the position after the second number in the original line.
			rest := line
			for i := 0; i < 2; i++ {
				rest = strings.TrimSpace(rest)
				idx := strings.IndexByte(rest, ' ')
				if idx < 0 {
					rest = ""
					break
				}
				rest = rest[idx:]
			}
			rest = strings.TrimSpace(rest)
			fields = []string{fields[0], fields[1], rest}
		}

		p, err1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		pp, err2 := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		args := strings.TrimSpace(fields[2])
		if args == "" {
			continue
		}
		children[pp] = append(children[pp], proc{pid: p, ppid: pp, args: args})
	}

	// Walk the tree from the root PID using BFS with depth tracking.
	var tree []string
	type entry struct {
		pid   int
		depth int
	}
	queue := []entry{{pid: pid, depth: 0}}
	for len(queue) > 0 && len(tree) < maxProcessTreeEntries {
		e := queue[0]
		queue = queue[1:]
		if e.depth >= maxProcessTreeDepth {
			continue
		}
		indent := strings.Repeat("  ", e.depth)
		for _, child := range children[e.pid] {
			if len(tree) >= maxProcessTreeEntries {
				break
			}
			tree = append(tree, indent+child.args)
			queue = append(queue, entry{pid: child.pid, depth: e.depth + 1})
		}
	}
	return tree
}

// parseTarget parses a tmux target string "session:window.pane" into a Pane.
func parseTarget(target string) (model.Pane, error) {
	// Split "session:window.pane"
	colonIdx := strings.LastIndex(target, ":")
	if colonIdx < 0 {
		return model.Pane{}, fmt.Errorf("invalid target %q: missing ':'", target)
	}

	session := target[:colonIdx]
	rest := target[colonIdx+1:]

	dotIdx := strings.LastIndex(rest, ".")
	if dotIdx < 0 {
		return model.Pane{}, fmt.Errorf("invalid target %q: missing '.'", target)
	}

	window, err := strconv.Atoi(rest[:dotIdx])
	if err != nil {
		return model.Pane{}, fmt.Errorf("invalid window index in %q: %w", target, err)
	}

	pane, err := strconv.Atoi(rest[dotIdx+1:])
	if err != nil {
		return model.Pane{}, fmt.Errorf("invalid pane index in %q: %w", target, err)
	}

	return model.Pane{
		Target:  target,
		Session: session,
		Window:  window,
		Pane:    pane,
	}, nil
}
