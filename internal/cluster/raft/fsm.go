package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// applyStore persists the DURABLE watermark: the highest index whose
	// effects are known to be in this node's SQLite. Nil disables the whole
	// mechanism (tests, the pre-DB factory path) and restores the old
	// replay-everything behaviour. See fsmApplyState.
	applyStore   AppliedIndexStore
	durableIndex atomic.Uint64
	// stalled latches when an applier failed NON-deterministically and the
	// retries could not clear it. From then on the watermark stops advancing,
	// so the next restart replays from the failure instead of skipping past a
	// write this node never made. Cleared by a restart, by a snapshot restore
	// (which supersedes whatever we missed), or when the snapshot hold gives up.
	stalled atomic.Bool
	// flushMu guards lastFlush, which throttles watermark writes: the exact
	// value lives in durableIndex, and persisting a slightly older one only
	// means replaying a few more (idempotent, recent) entries after a crash.
	flushMu   sync.Mutex
	lastFlush time.Time
	// stalledAt is when the stall started, bounding how long snapshotting is
	// held off (see Snapshot). Guarded by flushMu.
	stalledAt time.Time
}

// maxApplierRetries bounds how many times the SAME log index may fail to apply
// within this process lifetime before the FSM gives up and treats it as
// applied. See the poison-entry comment in Apply.
const maxApplierRetries = 3

// applierRetryAttempts / applierRetryBackoff bound the IN-PLACE retry of a
// non-deterministic applier failure (a statement timeout, SQLITE_BUSY, a
// momentarily unreachable Postgres). Raft calls Apply exactly once per index in
// steady state, so without a retry here a transient error meant the write was
// simply dropped while the index advanced — the node then diverged from the
// cluster silently and forever, with nothing to repair it. Deliberately short
// (~1.75s worst case): Apply runs on the single FSM goroutine, so a long stall
// would back up every other command.
const (
	applierRetryAttempts = 3
	applierRetryBackoff  = 250 * time.Millisecond
)

// applyStateFlushInterval throttles the durable-watermark write so a busy log
// does not add a row update per entry. Between flushes the watermark lags by at
// most this much, which only costs a few replayed (idempotent) entries.
const applyStateFlushInterval = 2 * time.Second

// applyStateWriteTimeout bounds a single watermark write. Failing it is not
// fatal: the in-memory value keeps advancing and the next flush catches up.
const applyStateWriteTimeout = 5 * time.Second

// stallSnapshotGrace is how long a stalled FSM holds snapshotting off so a
// restart still gets the chance to re-apply what it missed. Long enough to
// cover an update/restart cycle, short enough that a node left running cannot
// grow its Raft log for ever.
const stallSnapshotGrace = 15 * time.Minute

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
// reproduce on Restore. appliedIndex is the index that state corresponds to and
// must be recorded in the snapshot — Restore needs it to compare against a
// durable FSM's own position.
type Snapshotter interface {
	Snapshot(appliedIndex uint64) (hraft.FSMSnapshot, error)
}

// RestoreResult reports what a Restore actually did, so the FSM can keep its
// durable watermark honest.
type RestoreResult struct {
	// Index is the applied index the snapshot was taken at. 0 when the snapshot
	// predates the field, which means "unknown" — the caller must then assume
	// nothing and let the log replay rebuild from it.
	Index uint64
	// Skipped is true when the local state was already at or beyond Index and
	// was therefore KEPT, the stream merely drained.
	Skipped bool
}

// Restorer repopulates FSM-backed state from rc, unless localIndex shows the
// local state is already at or beyond the snapshot (then it keeps it and says
// so). localIndex 0 means "unknown" and always restores.
type Restorer interface {
	Restore(rc io.ReadCloser, localIndex uint64) (RestoreResult, error)
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

// SetAppliedIndexStore wires the durable apply watermark and loads its current
// value. Must be called before the Raft node starts. Without it the FSM keeps
// the historical behaviour: every entry after the last snapshot is re-applied
// on each restart.
func (f *FSM) SetAppliedIndexStore(s AppliedIndexStore) {
	if s == nil {
		return
	}
	f.mu.Lock()
	f.applyStore = s
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), applyStateWriteTimeout)
	defer cancel()
	idx, err := s.Load(ctx)
	if err != nil {
		// Unknown watermark: fall back to replaying everything (what this node
		// did before the watermark existed) rather than skipping entries we
		// cannot prove were applied.
		f.logger.Warn("raft FSM: could not read the applied-index watermark; log replay will re-apply from the last snapshot", "err", err)
		return
	}
	f.durableIndex.Store(idx)
	if idx > 0 {
		f.logger.Info("raft FSM: durable state watermark loaded", "applied_index", idx)
	}
}

// AppliedIndex returns the highest log index successfully passed through
// Apply (regardless of whether the underlying applier returned an error).
func (f *FSM) AppliedIndex() uint64 { return f.appliedIndex.Load() }

// DurableIndex returns the highest index whose effects are known to be in this
// node's own database — the point a restart resumes the log from. 0 when no
// watermark is wired or none has been written yet.
func (f *FSM) DurableIndex() uint64 { return f.durableIndex.Load() }

// Stalled reports that an applier failed non-deterministically and could not be
// retried through, so the watermark has stopped advancing. The node keeps
// serving; the entries after the stall are re-applied on the next restart.
func (f *FSM) Stalled() bool { return f.stalled.Load() }

// Apply implements raft.FSM. It decodes the Command envelope and dispatches
// to the registered applier. Return value is surfaced to the local caller via
// raft.ApplyFuture.Response — remote replicas ignore it.
//
// Entries this node's database already holds are SKIPPED (see alreadyApplied):
// our state is durable, so a restart's log replay must not re-run history.
func (f *FSM) Apply(rlog *hraft.Log) any {
	if f.alreadyApplied(rlog.Index) {
		// Already in our SQLite from a previous process. Advance the in-memory
		// index so status reporting tracks the log, and publish nothing — the
		// bridge shipped this entry the first time round.
		f.appliedIndex.Store(rlog.Index)
		return nil
	}

	var cmd Command
	if err := json.Unmarshal(rlog.Data, &cmd); err != nil {
		f.logger.Warn("raft FSM: failed to decode command", "index", rlog.Index, "err", err)
		f.appliedIndex.Store(rlog.Index)
		f.markApplied(rlog.Index) // deterministic: it fails identically everywhere
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
		f.markApplied(rlog.Index)
		return ErrUnknownCommand
	}

	err := f.runApplier(applier, cmd, rlog)
	f.appliedIndex.Store(rlog.Index)

	switch {
	case err == nil || errors.Is(err, ErrUnknownCommand):
		f.markApplied(rlog.Index)

	case isTransientApplyError(err):
		// A NON-deterministic failure that outlived its retries (a DB stall
		// under load, a long-running statement cancelled). Other replicas
		// applied this entry, so accepting it here would fork our state — the
		// exact way a cluster-wide "delete the duplicate host row" silently
		// stopped being true on one node. Keep the watermark where it is
		// instead: the entry is re-applied on the next restart.
		fails := f.countFailure(rlog.Index)
		if fails > maxApplierRetries {
			// Poison guard (see below): stop replaying it forever.
			f.logger.Error("raft FSM: log index repeatedly failed to apply; giving up and treating it as applied — this node may now differ from its peers for that record",
				"index", rlog.Index, "type", uint16(cmd.Type), "fails", fails, "err", err)
			f.markApplied(rlog.Index)
			err = nil
			break
		}
		if f.stalled.CompareAndSwap(false, true) {
			f.flushMu.Lock()
			f.stalledAt = time.Now()
			f.flushMu.Unlock()
			f.logger.Error("raft FSM: applier failed after retries; the durable-state watermark is frozen here and the log is re-applied from this entry on the next restart",
				"index", rlog.Index, "type", uint16(cmd.Type), "err", err)
		} else {
			f.logger.Warn("raft FSM: applier failed after retries while already stalled",
				"index", rlog.Index, "type", uint16(cmd.Type), "err", err)
		}

	default:
		// Deterministic (a constraint violation, a malformed payload): every
		// replica fails it identically, so the log index legitimately advances.
		//
		// Poison-entry guard. hashicorp/raft re-applies the same log index on
		// the next snapshot/restore or leader handoff. We once hit a poison
		// entry that failed deterministically on every replay and wedged the
		// apply pipeline (the prod incident where the log grew to ~793MB with
		// zero snapshots: snapshotting was impossible while an entry kept
		// failing, so the log could never be truncated). Scoped narrowly to
		// applier ERRORS — not panics (those stay fatal so a corrupt node
		// re-syncs from a snapshot).
		fails := f.countFailure(rlog.Index)
		f.logger.Warn("raft FSM: applier returned error",
			"index", rlog.Index, "type", uint16(cmd.Type), "fails", fails, "err", err)
		f.markApplied(rlog.Index)
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

// runApplier dispatches one command, retrying in place while the failure looks
// non-deterministic. See applierRetryAttempts for why the budget is small.
func (f *FSM) runApplier(applier CommandApplier, cmd Command, rlog *hraft.Log) error {
	err := applier(cmd, rlog)
	for attempt := 1; attempt <= applierRetryAttempts && isTransientApplyError(err); attempt++ {
		wait := applierRetryBackoff * time.Duration(1<<(attempt-1))
		f.logger.Warn("raft FSM: applier failed transiently; retrying",
			"index", rlog.Index, "type", uint16(cmd.Type), "attempt", attempt, "retry_in", wait, "err", err)
		time.Sleep(wait)
		err = applier(cmd, rlog)
	}
	return err
}

// alreadyApplied reports whether index is covered by the durable watermark.
//
// The watermark is not required to be contiguous with the log: raft keeps
// configuration and no-op entries out of the FSM entirely, so indexes legitimately
// skip in normal operation. Only "is this one already in our tables" matters here;
// a restore realigns the watermark explicitly (see Restore).
func (f *FSM) alreadyApplied(index uint64) bool {
	f.mu.RLock()
	store := f.applyStore
	f.mu.RUnlock()
	if store == nil {
		return false
	}
	durable := f.durableIndex.Load()
	if durable == 0 {
		return false // nothing proven yet — replay rather than skip
	}
	return index <= durable
}

// markApplied advances the durable watermark, flushing it to the store at most
// once per applyStateFlushInterval. A stalled FSM never advances it.
func (f *FSM) markApplied(index uint64) {
	f.mu.RLock()
	store := f.applyStore
	f.mu.RUnlock()
	if store == nil || f.stalled.Load() {
		return
	}
	if index <= f.durableIndex.Load() {
		return
	}
	f.durableIndex.Store(index)

	f.flushMu.Lock()
	due := f.lastFlush.IsZero() || time.Since(f.lastFlush) >= applyStateFlushInterval
	if due {
		f.lastFlush = time.Now()
	}
	f.flushMu.Unlock()
	if !due {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), applyStateWriteTimeout)
	defer cancel()
	if err := store.Store(ctx, index); err != nil {
		// Not fatal: the in-memory watermark keeps advancing and the next flush
		// re-writes it. A crash before then just replays a few recent entries.
		f.logger.Warn("raft FSM: could not persist the applied-index watermark", "index", index, "err", err)
	}
}

// countFailure records one more failure for index and returns the running
// count, keeping the bookkeeping map bounded.
func (f *FSM) countFailure(index uint64) int {
	f.failMu.Lock()
	defer f.failMu.Unlock()
	f.failCounts[index]++
	fails := f.failCounts[index]
	switch {
	case fails > maxApplierRetries:
		// Given up: it is treated as applied and never retried, so its counter
		// is dead weight — drop it.
		delete(f.failCounts, index)
	case len(f.failCounts) > maxFailCountsEntries:
		// Bound the map. It only needs to track indexes ACTIVELY failing in
		// their brief 1..maxApplierRetries retry window; a transient one-off
		// failure on an index that never re-applies would otherwise leave a
		// dead entry forever (slow unbounded growth). Clearing resets counts —
		// rare, and a genuinely poisoning index simply re-trips the guard after
		// a few more retries.
		f.failCounts = map[uint64]int{index: fails}
	}
	return fails
}

// transientApplyErrors are the substrings that mark a failure as a property of
// THIS node right now (load, locks, a cancelled statement) rather than of the
// command itself. Matched on the message because the error crosses GORM and
// several drivers, which do not preserve sentinel values.
var transientApplyErrors = []string{
	"context deadline exceeded",
	"context canceled",
	"database is locked",
	"database table is locked",
	"sqlite_busy",
	"database is busy",
	"deadlock detected",
	"too many connections",
	"connection refused",
	"connection reset",
	"broken pipe",
	"i/o timeout",
	"canceling statement due to user request",
	"server closed the connection",
}

// isTransientApplyError classifies an applier failure as non-deterministic —
// worth retrying, and never worth silently accepting.
func isTransientApplyError(err error) bool {
	if err == nil || errors.Is(err, ErrUnknownCommand) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range transientApplyErrors {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Snapshot implements raft.FSM by delegating to the wired Snapshotter, stamping
// the dump with the index our durable state actually corresponds to.
//
// While the FSM is stalled it declines, for a bounded while. A snapshot moves
// raft's own lastApplied past the entry we failed to apply, so the restart that
// was going to re-apply it would never be offered it again — the freeze would
// mean nothing. Raft simply logs the refusal and retries on its next interval,
// and the hold is released after stallSnapshotGrace so a node nobody restarts
// cannot grow its log without bound (the incident where a stuck entry made
// snapshotting impossible and the log reached ~793MB).
func (f *FSM) Snapshot() (hraft.FSMSnapshot, error) {
	if f.stalled.Load() {
		if held := f.holdSnapshotFor(); held > 0 {
			return nil, fmt.Errorf("raft FSM: apply is stalled at index %d; holding off the snapshot for another %s so a restart can re-apply it",
				f.durableIndex.Load()+1, held.Round(time.Second))
		}
		if f.stalled.CompareAndSwap(true, false) {
			f.logger.Error("raft FSM: apply stalled for too long; releasing the snapshot hold — the entry it could not apply is now permanently missing from this node",
				"watermark", f.durableIndex.Load(), "applied_index", f.appliedIndex.Load())
		}
	}
	f.mu.RLock()
	s := f.snapshotter
	f.mu.RUnlock()
	return s.Snapshot(f.durableIndex.Load())
}

// holdSnapshotFor returns how much of the stall grace period is left, or 0 when
// it has run out.
func (f *FSM) holdSnapshotFor() time.Duration {
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	if f.stalledAt.IsZero() {
		return 0
	}
	if left := stallSnapshotGrace - time.Since(f.stalledAt); left > 0 {
		return left
	}
	return 0
}

// Restore implements raft.FSM by delegating to the wired Restorer, then
// realigning the durable watermark with whatever the restore decided.
func (f *FSM) Restore(rc io.ReadCloser) error {
	f.mu.RLock()
	r := f.restorer
	store := f.applyStore
	f.mu.RUnlock()

	local := f.durableIndex.Load()
	if store == nil {
		local = 0 // no watermark to trust — always take the snapshot
	}
	res, err := r.Restore(rc, local)
	if err != nil {
		return err
	}
	switch {
	case res.Skipped:
		f.logger.Info("raft FSM: snapshot is behind this node's own state; keeping the local database",
			"snapshot_index", res.Index, "local_index", local)
	case res.Index > 0:
		// The tables ARE the snapshot now: the watermark is exactly its index.
		f.stalled.Store(false) // whatever we failed to apply is superseded
		f.setDurable(res.Index)
		f.logger.Info("raft FSM: state restored from snapshot", "applied_index", res.Index)
	default:
		// A snapshot from before the index was recorded: we cannot say where the
		// restored state sits, so claim nothing and let the replay rebuild.
		f.stalled.Store(false)
		f.setDurable(0)
		f.logger.Info("raft FSM: state restored from a snapshot with no recorded index; the log tail will be re-applied")
	}
	return nil
}

// setDurable forces the watermark to index and persists it immediately —
// used on the restore path, where the value must not be lost to the flush
// throttle before the next entry arrives.
func (f *FSM) setDurable(index uint64) {
	f.mu.RLock()
	store := f.applyStore
	f.mu.RUnlock()
	f.durableIndex.Store(index)
	if store == nil {
		return
	}
	f.flushMu.Lock()
	f.lastFlush = time.Now()
	f.flushMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), applyStateWriteTimeout)
	defer cancel()
	if err := store.Store(ctx, index); err != nil {
		f.logger.Warn("raft FSM: could not persist the applied-index watermark after a restore", "index", index, "err", err)
	}
}

// noopSnapshotter / noopRestorer are placeholders used until concrete
// SQLite-backed implementations are wired. They make the FSM safe to build
// and let the bootstrap path work end-to-end even before real persistence is
// hooked up.
type noopSnapshotter struct{}

func (noopSnapshotter) Snapshot(uint64) (hraft.FSMSnapshot, error) { return emptySnapshot{}, nil }

type noopRestorer struct{}

func (noopRestorer) Restore(rc io.ReadCloser, _ uint64) (RestoreResult, error) {
	_, _ = io.Copy(io.Discard, rc)
	return RestoreResult{}, rc.Close()
}

type emptySnapshot struct{}

func (emptySnapshot) Persist(sink hraft.SnapshotSink) error { return sink.Close() }
func (emptySnapshot) Release()                              {}
