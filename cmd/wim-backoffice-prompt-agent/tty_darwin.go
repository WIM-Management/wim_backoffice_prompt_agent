//go:build darwin

package main

import "golang.org/x/sys/unix"

// termios get/set ioctl requests on Darwin (BSD naming).
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
