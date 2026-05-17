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

// reuseAddrControl sets SO_REUSEADDR (and SO_REUSEPORT on Linux/Darwin) on
// a listening socket before bind. Combined with hashicorp/raft's
// NetworkTransport this lets a freshly-started process re-bind the Raft
// listen port even if a previous instance was SIGKILL'd less than the
// kernel's TIME_WAIT window ago (typical air hot-reload scenario).
func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
		// SO_REUSEPORT is the magic on Linux/Darwin that allows two
		// processes to share the same port; we use it here only as an
		// "ignore the kernel's TIME_WAIT bookkeeping for our previous
		// instance" hint. Ignoring failure is intentional — older
		// kernels may not have it.
		_ = setsockoptReusePort(int(fd))
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
