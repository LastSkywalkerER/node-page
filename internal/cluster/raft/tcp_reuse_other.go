//go:build !linux && !darwin

package raft

// setsockoptReusePort is a no-op on platforms that don't support
// SO_REUSEPORT. SO_REUSEADDR (set unconditionally in reuseAddrControl)
// is still enough on most systems to recover from an unclean restart.
func setsockoptReusePort(fd int) error { return nil }
