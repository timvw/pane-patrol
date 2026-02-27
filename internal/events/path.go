package events

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultSocketPath() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		return filepath.Join(runtimeDir, "pane-patrol", "events.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("pane-patrol-%d", os.Getuid()), "events.sock")
}
