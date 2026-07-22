//go:build !windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
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
// a longer line is silently truncated at the cap (or stalls waiting for a
// newline that can't fit). Clearing ICANON in-process on the exact fd we read
// removes the cap while keeping echo, signals, and CR→NL translation, so the
// bufio ReadString('\n') in PasteIDToken works unchanged. Best-effort: if r
// isn't a real tty the ioctl fails and we return a no-op (tokens under the cap
// still enroll — no regression).
func configurePasteInput(r io.Reader) func() {
	f, ok := r.(*os.File)
	if !ok {
		return func() {}
	}
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return func() {}
	}
	raw := *old
	raw.Lflag &^= unix.ICANON
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return func() {}
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, old) }
}
