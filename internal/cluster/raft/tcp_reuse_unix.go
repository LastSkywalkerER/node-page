//go:build !windows

package raft

import "syscall"

// reuseAddrControl sets SO_REUSEADDR on a listening socket before bind.
// Combined with hashicorp/raft's NetworkTransport this lets a freshly-
// started process re-bind the Raft listen port even if a previous
// instance was killed less than the kernel's TIME_WAIT window ago
// (typical air hot-reload scenario).
//
// We deliberately do NOT set SO_REUSEPORT — that lets two LIVE
// processes share the same listening port, and the kernel load-balances
// incoming connections between them. If air leaves a zombie process
// holding the port, both old and new would happily bind and traffic
// would be split unpredictably (probe from one process could land on
// the other and hang). SO_REUSEADDR alone is enough for the unclean-
// shutdown case we actually care about.
func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
	})
	if err != nil {
		return err
	}
	return setErr
}
