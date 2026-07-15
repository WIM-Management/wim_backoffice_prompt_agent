// Package agentlog is the daemon-path log sink. Scheduled `run-once` invocations
// have no attached console (Windows is built -H=windowsgui; macOS launchd/systemd
// discard stdio), so their diagnostics are written to a file instead. Interactive
// commands (enroll/install/status/update) keep printing to stdout/stderr directly.
//
// Every call is best-effort: a missing path, permission error, or full disk is
// silently ignored so logging never breaks collection or self-update.
package agentlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxLogBytes caps agent.log; on overflow it rolls over to agent.log.1 (one
// generation kept). Small cap — this is low-volume diagnostic output.
const maxLogBytes = 5 << 20 // 5 MB

var (
	mu   sync.Mutex
	path string // "" until Setup runs → Printf is a no-op
)

// Setup points the daemon logger at <dir>/agent.log and best-effort creates dir.
// Call once at startup before any daemon-path work.
func Setup(dir string) {
	mu.Lock()
	defer mu.Unlock()
	_ = os.MkdirAll(dir, 0o700)
	path = filepath.Join(dir, "agent.log")
}

// Printf appends one timestamped line to the agent log. Best-effort (see package
// doc): all errors are swallowed.
func Printf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return
	}
	rotateIfNeeded()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "%s %s\n", ts, fmt.Sprintf(format, args...))
}

// rotateIfNeeded renames path -> path.1 (single generation) once it exceeds the
// size cap. Caller holds mu.
func rotateIfNeeded() {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < maxLogBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}
