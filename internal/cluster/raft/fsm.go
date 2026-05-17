package raft

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	hraft "github.com/hashicorp/raft"
)

// CommandApplier handles a single Command type during FSM.Apply.
//
// Implementations are registered with FSM.Register on construction. The given
// ctx is already flagged via WithApplier so repository writers gated by
// WriteGate will allow the underlying SQL writes.
//
// Returning a non-nil error is treated as a DETERMINISTIC failure (e.g. a
// constraint violation): it is logged on every replica but the log index
// still advances identically. Non-deterministic errors (disk full, ctx
// cancellation) must panic so the node crashes and re-syncs from a snapshot.
type CommandApplier func(cmd Command, log *hraft.Log) error

// FSM is the HashiCorp Raft state machine for node-stats. It delegates each
// Command to a registered CommandApplier — the actual write logic lives in
// the existing repositories so we reuse all current persistence code.
//
// The state itself lives in SQLite (accessed by the appliers), not inside the
// FSM struct. Snapshot/Restore serialise the relevant tables under a single
// transaction.
type FSM struct {
	logger       *log.Logger
	mu           sync.RWMutex
	appliers     map[CommandType]CommandApplier
	appliedIndex atomic.Uint64

	// snapshotter / restorer are set later — they need DB access wired in.
	snapshotter Snapshotter
	restorer    Restorer
}

// Snapshotter produces a serialisable snapshot of the FSM-backed state. It
// must capture state at the current applied index under a consistent read
// (e.g. SQLite BEGIN IMMEDIATE) so the resulting bytes deterministically
// reproduce on Restore.
type Snapshotter interface {
	Snapshot() (hraft.FSMSnapshot, error)
}

// Restorer wipes any existing FSM-backed state and repopulates it from rc.
type Restorer interface {
	Restore(rc io.ReadCloser) error
}

// NewFSM builds an empty FSM. Appliers are registered via Register before the
// Raft node starts. Snapshotter / Restorer default to a no-op until wired.
func NewFSM(logger *log.Logger) *FSM {
	return &FSM{
		logger:      logger,
		appliers:    make(map[CommandType]CommandApplier),
		snapshotter: noopSnapshotter{},
		restorer:    noopRestorer{},
	}
}

// Register associates an applier with a command type. Must be called before
// the Raft node is started.
func (f *FSM) Register(t CommandType, fn CommandApplier) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliers[t] = fn
}

// SetSnapshotter wires the snapshot producer.
func (f *FSM) SetSnapshotter(s Snapshotter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotter = s
}

// SetRestorer wires the restore consumer.
func (f *FSM) SetRestorer(r Restorer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restorer = r
}

// AppliedIndex returns the highest log index successfully passed through
// Apply (regardless of whether the underlying applier returned an error).
func (f *FSM) AppliedIndex() uint64 { return f.appliedIndex.Load() }

// Apply implements raft.FSM. It decodes the Command envelope and dispatches
// to the registered applier. Return value is surfaced to the local caller via
// raft.ApplyFuture.Response — remote replicas ignore it.
func (f *FSM) Apply(rlog *hraft.Log) any {
	var cmd Command
	if err := json.Unmarshal(rlog.Data, &cmd); err != nil {
		f.logger.Warn("raft FSM: failed to decode command", "index", rlog.Index, "err", err)
		f.appliedIndex.Store(rlog.Index)
		return fmt.Errorf("decode command: %w", err)
	}

	f.mu.RLock()
	applier, ok := f.appliers[cmd.Type]
	f.mu.RUnlock()

	if !ok {
		f.logger.Warn("raft FSM: unknown command type",
			"index", rlog.Index, "type", uint16(cmd.Type),
			"origin_cluster", cmd.OriginClusterID, "origin_node", cmd.OriginNodeID)
		f.appliedIndex.Store(rlog.Index)
		return ErrUnknownCommand
	}

	err := applier(cmd, rlog)
	f.appliedIndex.Store(rlog.Index)

	if err != nil && !errors.Is(err, ErrUnknownCommand) {
		// Deterministic vs non-deterministic classification is the applier's
		// responsibility (it panics for non-deterministic failures). At this
		// layer we only log and return.
		f.logger.Warn("raft FSM: applier returned error",
			"index", rlog.Index, "type", uint16(cmd.Type), "err", err)
	}
	return err
}

// Snapshot implements raft.FSM by delegating to the wired Snapshotter.
func (f *FSM) Snapshot() (hraft.FSMSnapshot, error) {
	f.mu.RLock()
	s := f.snapshotter
	f.mu.RUnlock()
	return s.Snapshot()
}

// Restore implements raft.FSM by delegating to the wired Restorer.
func (f *FSM) Restore(rc io.ReadCloser) error {
	f.mu.RLock()
	r := f.restorer
	f.mu.RUnlock()
	return r.Restore(rc)
}

// noopSnapshotter / noopRestorer are placeholders used until concrete
// SQLite-backed implementations are wired. They make the FSM safe to build
// and let the bootstrap path work end-to-end even before real persistence is
// hooked up.
type noopSnapshotter struct{}

func (noopSnapshotter) Snapshot() (hraft.FSMSnapshot, error) { return emptySnapshot{}, nil }

type noopRestorer struct{}

func (noopRestorer) Restore(rc io.ReadCloser) error {
	_, _ = io.Copy(io.Discard, rc)
	return rc.Close()
}

type emptySnapshot struct{}

func (emptySnapshot) Persist(sink hraft.SnapshotSink) error { return sink.Close() }
func (emptySnapshot) Release()                              {}
