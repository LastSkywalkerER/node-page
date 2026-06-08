//go:build windows

package raft

import "syscall"

// reuseAddrControl is intentionally a no-op on Windows.
//
// On Windows SO_REUSEADDR has the semantics of unix's SO_REUSEPORT: it lets
// two LIVE processes bind the same port simultaneously, with connections split
// unpredictably between them. That is exactly the failure mode the unix variant
// guards against (see tcp_reuse_unix.go). We therefore skip the socket option
// on Windows, trading the fast-rebind-after-unclean-shutdown optimisation (an
// air hot-reload nicety that doesn't apply to Windows deployments) for the
// single-binder guarantee.
func reuseAddrControl(network, address string, c syscall.RawConn) error { return nil }
