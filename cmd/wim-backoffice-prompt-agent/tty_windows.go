//go:build windows

package main

import (
	"io"
	"os"
)

// openPasteInput returns os.Stdin, which attachParentConsole() has already
// reattached to the parent terminal's CONIN$ (the -H=windowsgui build has no
// console of its own). Wrapped so the caller can Close() uniformly without
// closing the process's real stdin.
func openPasteInput() (io.ReadCloser, error) {
	return io.NopCloser(os.Stdin), nil
}
