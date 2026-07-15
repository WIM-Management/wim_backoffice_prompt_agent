//go:build windows

package main

import (
	"os"
	"syscall"
)

// attachParentProcess == ATTACH_PARENT_PROCESS (DWORD)-1.
const attachParentProcess = uintptr(0xffffffff)

// attachParentConsole reconnects stdio to the parent terminal's console when the
// process was launched from one. The release binary is built with -H=windowsgui
// so it has NO console of its own — without this, interactive commands (e.g.
// `enroll`, which prints the Google OAuth URL the user must open) would produce
// no visible output. When launched by Task Scheduler there is no parent console,
// AttachConsole fails, and this is a silent no-op — keeping scheduled `run-once`
// runs windowless (the whole point of the GUI subsystem build).
func attachParentConsole() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(attachParentProcess); r == 0 {
		return // no parent console (scheduled run) — stay silent
	}
	// Reopen the standard handles onto the now-attached console.
	if h, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = h
		os.Stderr = h
	}
	if h, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = h
	}
}
