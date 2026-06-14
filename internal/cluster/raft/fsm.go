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

	// applyEvents is a non-blocking notification channel consumed by the
	// cross-cluster bridge sender. Each applied log entry is published
	// here as an ApplyEvent; a slow consumer is allowed to drop oldest
	// (the bridge falls back to snapshot replay on extended drops).
	applyEvents chan ApplyEvent

	// failCounts tracks how many times a given log index's applier has
	// returned an error within this process lifetime — poison-entry guard
	// (see Apply). Guarded by its own mutex to stay off the appliers RWLock
	// hot path.
	failMu     sync.Mutex
	failCounts map[uint64]int
}

// maxApplierRetries bounds how many times the SAME log index may fail to apply
// within this process lifetime before the FSM gives up and treats it as
// applied. See the poison-entry comment in Apply.
const maxApplierRetries = 3

// maxFailCountsEntries bounds the poison-entry counter map so transient one-off
// apply failures can't accumulate a dead entry per index forever. Far above any
// plausible number of concurrently-retrying indexes; hitting it means something
// is badly wrong and a reset is harmless (see Apply).
const maxFailCountsEntries = 4096

// ApplyEvent is the per-apply notification published to FSM.ApplyEvents.
// It carries the decoded Command envelope plus the Raft log index so the
// bridge sender can ship the entry verbatim to the peer cluster.
type ApplyEvent struct {
	Index uint64
	Cmd   Command
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
		applyEvents: make(chan ApplyEvent, 1024),
		failCounts:  make(map[uint64]int),
	}
}

// ApplyEvents returns the channel readers (e.g. the cross-cluster bridge
// sender) consume to learn about FSM applies in near-real-time.
func (f *FSM) ApplyEvents() <-chan ApplyEvent { return f.applyEvents }

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

		// Poison-entry guard. hashicorp/raft re-applies the same log index on
		// the next snapshot/restore or leader handoff. We once hit a poison
		// entry that failed deterministically on every replay and wedged the
		// apply pipeline (the prod incident where the log grew to ~793MB with
		// zero snapshots: snapshotting was impossible while an entry kept
		// failing, so the log could never be truncated). Scoped narrowly to
		// applier ERRORS — not panics (those stay fatal so a corrupt node
		// re-syncs from a snapshot): if the SAME index has failed more than
		// maxApplierRetries times this process lifetime, log loudly and treat
		// it as applied (return nil) so it can never block the pipeline again.
		f.failMu.Lock()
		f.failCounts[rlog.Index]++
		fails := f.failCounts[rlog.Index]
		switch {
		case fails > maxApplierRetries:
			// Given up: it is treated as applied below and never retried, so its
			// counter is dead weight — drop it.
			delete(f.failCounts, rlog.Index)
		case len(f.failCounts) > maxFailCountsEntries:
			// Bound the map. It only needs to track indexes ACTIVELY failing in
			// their brief 1..maxApplierRetries retry window; a transient one-off
			// failure on an index that never re-applies would otherwise leave a
			// dead entry forever (slow unbounded growth). Clearing resets counts —
			// rare, and a genuinely poisoning index simply re-trips the guard after
			// a few more retries.
			f.failCounts = map[uint64]int{rlog.Index: fails}
		}
		f.failMu.Unlock()
		if fails > maxApplierRetries {
			f.logger.Warn("raft FSM: log index repeatedly failed to apply; treating as applied to avoid wedging the pipeline (poison entry)",
				"index", rlog.Index, "type", uint16(cmd.Type), "fails", fails, "err", err)
			err = nil
		}
	}

	// Publish apply event for the cross-cluster bridge. Non-blocking: if
	// the buffered channel is full (consumer is way behind) drop oldest
	// rather than stall the FSM. Sustained drops mean the bridge must
	// catch up via the next snapshot replay.
	ev := ApplyEvent{Index: rlog.Index, Cmd: cmd}
	select {
	case f.applyEvents <- ev:
	default:
		select {
		case <-f.applyEvents:
			f.applyEvents <- ev
		default:
		}
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
