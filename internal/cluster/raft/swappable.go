package raft

import (
	"context"
	"sync/atomic"
	"time"
)

// SwappableService is a Service whose inner implementation can be replaced
// atomically at runtime. It exists so the rest of the app can hold a stable
// reference (via the Service interface) while the setup wizard / admin
// panel flips the layer between DisabledService and a real, fully-wired
// Node without process restart.
//
// Every interface method delegates to the currently-installed inner Service
// via a single atomic load — no lock on the hot read path.
type SwappableService struct {
	p atomic.Pointer[Service]
}

// NewSwappableService wraps the given initial Service. Pass NewDisabledService()
// for the "Raft is off at boot" case.
func NewSwappableService(initial Service) *SwappableService {
	s := &SwappableService{}
	s.Swap(initial)
	return s
}

// Swap atomically replaces the inner Service. The previous inner Service is
// NOT Closed by Swap — callers are responsible for that ordering.
func (s *SwappableService) Swap(svc Service) {
	if svc == nil {
		svc = NewDisabledService()
	}
	s.p.Store(&svc)
}

// Inner returns the currently-installed Service. Useful for callers that
// need to call concrete methods that aren't on the Service interface (the
// hashicorp Raft node Start hook, for instance, lives on *Node not Service).
func (s *SwappableService) Inner() Service {
	v := s.p.Load()
	if v == nil {
		return NewDisabledService()
	}
	return *v
}

// SubmitCommand delegates to the current inner Service.
func (s *SwappableService) SubmitCommand(ctx context.Context, cmd Command, timeout time.Duration) (SubmitResult, error) {
	return s.Inner().SubmitCommand(ctx, cmd, timeout)
}

// Status delegates to the current inner Service.
func (s *SwappableService) Status() Status { return s.Inner().Status() }

// Enabled delegates to the current inner Service.
func (s *SwappableService) Enabled() bool { return s.Inner().Enabled() }

// IsLeader delegates to the current inner Service.
func (s *SwappableService) IsLeader() bool { return s.Inner().IsLeader() }

// AddVoter delegates to the current inner Service.
func (s *SwappableService) AddVoter(id, addr string) error { return s.Inner().AddVoter(id, addr) }

// AddNonvoter delegates to the current inner Service.
func (s *SwappableService) AddNonvoter(id, addr string) error { return s.Inner().AddNonvoter(id, addr) }

// DemoteVoter delegates to the current inner Service.
func (s *SwappableService) DemoteVoter(id string) error { return s.Inner().DemoteVoter(id) }

// RemovePeer delegates to the current inner Service.
func (s *SwappableService) RemovePeer(id string) error { return s.Inner().RemovePeer(id) }

// TransferLeadership delegates to the current inner Service.
func (s *SwappableService) TransferLeadership() error { return s.Inner().TransferLeadership() }

// Stats delegates to the current inner Service.
func (s *SwappableService) Stats() map[string]string { return s.Inner().Stats() }

// Close shuts the currently-installed inner Service down.
func (s *SwappableService) Close() error { return s.Inner().Close() }
