//go:build !windows

package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// openPasteInput opens the controlling terminal for reading the pasted enroll
// token. Reading os.Stdin would fail under `curl -fsSL install.sh | bash`
// because the child's stdin is the script pipe, not the keyboard — /dev/tty is
// the real terminal and stays available in an interactive shell.
func openPasteInput() (io.ReadCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDONLY, 0)
}

// configurePasteInput switches the terminal to non-canonical mode for the paste
// read, returning a restore func. A Google id_token JWT is >1KB, but a canonical
// terminal caps a single input line at MAX_CANON (1024 bytes on macOS); pasting
// a longer line stalls forever because the trailing newline never enters the
// line buffer. Disabling ICANON (via stty, keeping echo/signals/ICRNL) removes
// the line-length cap. Best-effort: if r isn't a real tty or stty is missing we
// return a no-op — enroll still works for tokens under the cap (no regression).
func configurePasteInput(r io.Reader) func() {
	f, ok := r.(*os.File)
	if !ok {
		return func() {}
	}
	saved, err := runStty(f, "-g")
	if err != nil {
		return func() {}
	}
	if _, err := runStty(f, "-icanon", "min", "1", "time", "0"); err != nil {
		return func() {}
	}
	return func() { _, _ = runStty(f, strings.TrimSpace(saved)) }
}

// runStty runs `stty <args>` against the given terminal (its stdin), returning
// stdout. Used to read (`-g`) and set the terminal's line discipline.
func runStty(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	out, err := cmd.Output()
	return string(out), err
}
