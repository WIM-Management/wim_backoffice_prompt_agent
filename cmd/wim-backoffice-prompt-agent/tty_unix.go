//go:build !windows

package main

import (
	"io"
	"os"
)

// openPasteInput opens the controlling terminal for reading the pasted enroll
// token. Reading os.Stdin would fail under `curl -fsSL install.sh | bash`
// because the child's stdin is the script pipe, not the keyboard — /dev/tty is
// the real terminal and stays available in an interactive shell.
func openPasteInput() (io.ReadCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDONLY, 0)
}
