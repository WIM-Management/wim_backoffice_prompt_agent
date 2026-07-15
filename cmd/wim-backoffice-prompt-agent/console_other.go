//go:build !windows

package main

// attachParentConsole is a Windows-only concern (the -H=windowsgui build drops
// the console). On every other OS the process keeps its inherited stdio, so this
// is a no-op.
func attachParentConsole() {}
