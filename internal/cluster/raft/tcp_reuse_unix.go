//go:build linux || darwin

package raft

import "syscall"

// setsockoptReusePort installs SO_REUSEPORT on the given fd. Best-effort —
// returns an error which the caller may ignore for kernel versions that
// don't support it.
func setsockoptReusePort(fd int) error {
	// SO_REUSEPORT = 0x0F on linux, 0x0200 on darwin. Use the constant
	// from the unix package to stay portable across kernels.
	return syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, soReusePort, 1)
}
