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
// Walks up to maxProcessTreeDepth levels deep to capture subprocesses spawned by agents.
// Returns nil on any error — process info is best-effort, never fatal.
const maxProcessTreeDepth = 3

func getProcessTree(pid int) []string {
	if pid <= 0 {
		return nil
	}
	return collectProcessTree(pid, 0)
}

// collectProcessTree recursively collects child process command lines.
// Each level of nesting is indented with two additional spaces.
func collectProcessTree(pid, depth int) []string {
	if pid <= 0 || depth >= maxProcessTreeDepth {
		return nil
	}

	// pgrep -P finds direct children of the given PID
	cmd := exec.Command("pgrep", "-P", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	indent := strings.Repeat("  ", depth)
	var tree []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		childPID := strings.TrimSpace(line)
		if childPID == "" {
			continue
		}
		// Get the full command line of this child process
		ps := exec.Command("ps", "-o", "args=", "-p", childPID)
		psOut, err := ps.Output()
		if err != nil {
			continue
		}
		args := strings.TrimSpace(string(psOut))
		if args != "" {
			tree = append(tree, indent+args)
		}
		// Recurse into grandchildren
		cpid, err := strconv.Atoi(childPID)
		if err == nil {
			tree = append(tree, collectProcessTree(cpid, depth+1)...)
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
