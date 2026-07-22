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

// configurePasteInput is a no-op on Windows. The macOS MAX_CANON line cap that
// stalls a long paste doesn't apply the same way here, and CONIN$ mode tweaking
// would need a different (Win32 console) mechanism; add it only if a Windows
// long-paste stall is actually observed.
func configurePasteInput(_ io.Reader) func() { return func() {} }
