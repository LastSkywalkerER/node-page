package raft

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	hraft "github.com/hashicorp/raft"
)

// reuseAddrControl (defined per-platform in tcp_reuse_unix.go /
// tcp_reuse_windows.go) sets SO_REUSEADDR on a listening socket before bind so
// a freshly-started process can re-bind the Raft port even if a previous
// instance was killed within the kernel's TIME_WAIT window (the air hot-reload
// case). It is a no-op on Windows — see tcp_reuse_windows.go for why.

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
