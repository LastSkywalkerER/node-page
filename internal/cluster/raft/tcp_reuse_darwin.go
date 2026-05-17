//go:build darwin

package raft

// SO_REUSEPORT — Darwin value (FreeBSD-derived).
const soReusePort = 0x0200
