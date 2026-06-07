package raft

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	hraft "github.com/hashicorp/raft"
)

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

// reuseAddrStreamLayer is the hashicorp/raft StreamLayer that mirrors
// the default TCPStreamLayer but installs the SO_REUSEADDR socket
// option on the listener.
type reuseAddrStreamLayer struct {
	advertise net.Addr
	listener  *net.TCPListener
}

// newReuseAddrTCPTransport is a drop-in replacement for
// hraft.NewTCPTransport that uses the reuseAddrStreamLayer so the bind
// survives an unclean restart of the previous process.
func newReuseAddrTCPTransport(bindAddr string, advertise net.Addr, maxPool int, timeout time.Duration) (*hraft.NetworkTransport, *reuseAddrStreamLayer, error) {
	if advertise == nil {
		return nil, nil, errors.New("raft: advertise address is required")
	}
	lc := net.ListenConfig{Control: reuseAddrControl}
	l, err := lc.Listen(context.Background(), "tcp", bindAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen tcp %s: %w", bindAddr, err)
	}
	tl, ok := l.(*net.TCPListener)
	if !ok {
		_ = l.Close()
		return nil, nil, fmt.Errorf("raft: expected *net.TCPListener, got %T", l)
	}
	sl := &reuseAddrStreamLayer{advertise: advertise, listener: tl}
	cfg := &hraft.NetworkTransportConfig{
		Stream:                sl,
		MaxPool:               maxPool,
		Timeout:               timeout,
		ServerAddressProvider: nil,
	}
	tr := hraft.NewNetworkTransportWithConfig(cfg)
	return tr, sl, nil
}

// Dial implements hraft.StreamLayer.
func (l *reuseAddrStreamLayer) Dial(address hraft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", string(address), timeout)
}

// Accept implements hraft.StreamLayer.
func (l *reuseAddrStreamLayer) Accept() (net.Conn, error) { return l.listener.Accept() }

// Close implements hraft.StreamLayer.
func (l *reuseAddrStreamLayer) Close() error { return l.listener.Close() }

// Addr implements hraft.StreamLayer.
func (l *reuseAddrStreamLayer) Addr() net.Addr { return l.advertise }
