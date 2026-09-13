package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	hraft "github.com/hashicorp/raft"

	"system-stats/internal/app/config"
)

func newTestLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.NewWithOptions(io.Discard, log.Options{})
}

func TestFSM_AppliesRegisteredCommand(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))

	var sawType CommandType
	var sawCtxIsApplier bool
	var sawTimestamp time.Time
	fsm.Register(CmdHostUpsert, func(cmd Command, _ *hraft.Log) error {
		sawType = cmd.Type
		sawTimestamp = cmd.Timestamp
		// Applier handlers don't receive a ctx directly — the gate check
		// happens inside the applier's own helper code, which must build
		// the context with WithApplier. We verify the helper here.
		ctx := WithApplier(context.Background())
		sawCtxIsApplier = IsApplier(ctx)
		return nil
	})

	ts := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(Command{Type: CmdHostUpsert, Timestamp: ts, Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	res := fsm.Apply(&hraft.Log{Index: 7, Data: data})
	if res != nil {
		t.Fatalf("expected nil response from Apply, got %v", res)
	}
	if sawType != CmdHostUpsert {
		t.Fatalf("applier saw type %d, want %d", sawType, CmdHostUpsert)
	}
	if !sawTimestamp.Equal(ts) {
		t.Fatalf("applier saw timestamp %v, want %v", sawTimestamp, ts)
	}
	if !sawCtxIsApplier {
		t.Fatalf("WithApplier-derived context was not recognised by IsApplier")
	}
	if got := fsm.AppliedIndex(); got != 7 {
		t.Fatalf("AppliedIndex=%d after Apply, want 7", got)
	}
}

func TestFSM_UnknownCommandIsDeterministic(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))

	data, err := json.Marshal(Command{Type: 9999, Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res := fsm.Apply(&hraft.Log{Index: 1, Data: data})
	if res != ErrUnknownCommand {
		t.Fatalf("expected ErrUnknownCommand, got %v", res)
	}
	if got := fsm.AppliedIndex(); got != 1 {
		t.Fatalf("AppliedIndex must still advance on unknown commands; got %d, want 1", got)
	}
}

func TestFSM_GarbledPayloadDoesNotPanic(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	res := fsm.Apply(&hraft.Log{Index: 42, Data: []byte("not-json")})
	if res == nil {
		t.Fatal("expected non-nil error response for garbled payload")
	}
	if !strings.Contains(res.(error).Error(), "decode") {
		t.Fatalf("expected decode error, got %v", res)
	}
	if got := fsm.AppliedIndex(); got != 42 {
		t.Fatalf("AppliedIndex must advance even on decode error; got %d", got)
	}
}

func TestFSM_PoisonEntryGuard(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))

	wantErr := errors.New("deterministic boom")
	calls := 0
	fsm.Register(CmdHostUpsert, func(Command, *hraft.Log) error {
		calls++
		return wantErr
	})

	data, err := json.Marshal(Command{Type: CmdHostUpsert, Timestamp: time.Now(), Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The same index is replayed (snapshot/restore, leader handoff). The first
	// maxApplierRetries failures surface the error; once it has failed MORE than
	// maxApplierRetries times the FSM treats it as applied (nil) so it can't
	// wedge the pipeline.
	for i := 1; i <= maxApplierRetries; i++ {
		res := fsm.Apply(&hraft.Log{Index: 100, Data: data})
		if res == nil {
			t.Fatalf("apply #%d: expected the applier error to surface, got nil", i)
		}
	}
	res := fsm.Apply(&hraft.Log{Index: 100, Data: data})
	if res != nil {
		t.Fatalf("after exceeding maxApplierRetries the poison entry must be treated as applied (nil), got %v", res)
	}
	// A DIFFERENT index keeps its own independent failure budget.
	if res := fsm.Apply(&hraft.Log{Index: 101, Data: data}); res == nil {
		t.Fatal("a different index must not inherit the poisoned index's fail count")
	}
	if calls == 0 {
		t.Fatal("applier was never invoked")
	}
}

func TestWriteGate_OpenAlwaysAllows(t *testing.T) {
	t.Parallel()
	g := NewWriteGate(false)
	if _, ok := g.(OpenGate); !ok {
		t.Fatalf("expected OpenGate when raft disabled, got %T", g)
	}
	if !g.Allow(context.Background()) {
		t.Fatal("OpenGate must allow plain contexts")
	}
	if !g.Allow(WithApplier(context.Background())) {
		t.Fatal("OpenGate must allow applier contexts too")
	}
}

func TestWriteGate_RaftGateOnlyAllowsApplier(t *testing.T) {
	t.Parallel()
	g := NewWriteGate(true)
	if g.Allow(context.Background()) {
		t.Fatal("raftGate must reject non-applier contexts")
	}
	if !g.Allow(WithApplier(context.Background())) {
		t.Fatal("raftGate must allow applier contexts")
	}
}

func TestDisabledService_ReportsDisabled(t *testing.T) {
	t.Parallel()
	s := NewDisabledService()
	if s.Enabled() {
		t.Fatal("DisabledService.Enabled() returned true")
	}
	st := s.Status()
	if st.Enabled {
		t.Fatal("DisabledService.Status().Enabled == true")
	}
	_, err := s.SubmitCommand(context.Background(), Command{Type: CmdHostUpsert}, time.Second)
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Smoke-test the real Node end-to-end as a single-voter bootstrap cluster.
// This exercises the actual hashicorp/raft library, BoltDB stores, TCP
// transport bind, FSM Apply round-trip and graceful shutdown.
func TestNode_SingleVoterBootstrapAppliesCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	t.Parallel()

	dir, err := os.MkdirTemp("", "raft-node-test-*")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Find a free TCP port for the Raft transport. We can't use ":0" with
	// hashicorp's TCPTransport because it later needs a routable advertise
	// address; pre-binding lets us tell it the exact port and release it
	// before the library binds the listener.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	cfg := config.RaftConfig{
		Enabled:       true,
		ClusterID:     "test",
		NodeID:        "node-1",
		BindAddr:      addr,
		AdvertiseAddr: addr,
		DataDir:       dir,
		Bootstrap:     true,
	}
	fsm := NewFSM(newTestLogger(t))
	gotPayload := make(chan []byte, 1)
	fsm.Register(CmdConfigSet, func(cmd Command, _ *hraft.Log) error {
		gotPayload <- cmd.Payload
		return nil
	})

	node := NewNode(newTestLogger(t), cfg, fsm)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = node.Close() })

	// Wait until the single-voter cluster elects itself leader. The
	// hashicorp library always elects within HeartbeatTimeout when there
	// are no peers, but the default is ~1s — be generous.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if node.Status().State == "Leader" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st := node.Status(); st.State != "Leader" {
		t.Fatalf("single-voter cluster failed to elect leader; state=%q", st.State)
	}

	payload := []byte(`{"key":"hello","value":"world"}`)
	res, err := node.SubmitCommand(context.Background(), Command{
		Type:    CmdConfigSet,
		Payload: payload,
	}, 3*time.Second)
	if err != nil {
		t.Fatalf("SubmitCommand: %v", err)
	}
	if !res.Applied {
		t.Fatalf("SubmitCommand returned non-applied result: %+v", res)
	}

	select {
	case got := <-gotPayload:
		if string(got) != string(payload) {
			t.Fatalf("applier got payload %q, want %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("applier was not invoked within 2s of SubmitCommand returning")
	}
}

// memAppliedIndexStore is an AppliedIndexStore that keeps the watermark in RAM,
// standing in for the SQLite-backed one.
type memAppliedIndexStore struct {
	idx    uint64
	writes int
	err    error
}

func (m *memAppliedIndexStore) Load(context.Context) (uint64, error) { return m.idx, m.err }
func (m *memAppliedIndexStore) Store(_ context.Context, i uint64) error {
	if m.err != nil {
		return m.err
	}
	m.idx = i
	m.writes++
	return nil
}

func mustCommand(t *testing.T, typ CommandType) []byte {
	t.Helper()
	data, err := json.Marshal(Command{Type: typ, Timestamp: time.Now(), Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// The FSM's state is durable, so raft's post-restart replay must not re-run
// entries the database already holds. Re-applying old history is how a machine
// that moved (and changed its stable identity) reappeared as a second, ghost
// host row days later.
func TestFSM_SkipsEntriesBelowDurableWatermark(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	fsm.SetAppliedIndexStore(&memAppliedIndexStore{idx: 100})

	applied := 0
	fsm.Register(CmdHostUpsert, func(Command, *hraft.Log) error { applied++; return nil })
	data := mustCommand(t, CmdHostUpsert)

	for _, idx := range []uint64{98, 99, 100} {
		if res := fsm.Apply(&hraft.Log{Index: idx, Data: data}); res != nil {
			t.Fatalf("replayed index %d returned %v", idx, res)
		}
	}
	if applied != 0 {
		t.Fatalf("replayed entries ran the applier %d times, want 0", applied)
	}
	if got := fsm.AppliedIndex(); got != 100 {
		t.Fatalf("AppliedIndex=%d after replay, want 100 (it must track the log)", got)
	}

	if res := fsm.Apply(&hraft.Log{Index: 101, Data: data}); res != nil {
		t.Fatalf("new entry returned %v", res)
	}
	if applied != 1 {
		t.Fatalf("new entry ran the applier %d times, want 1", applied)
	}
}

// A gap in the log means raft installed a snapshot: everything below the entry
// it resumes at is already in the restored tables.
func TestFSM_FastForwardsWatermarkAfterSnapshotGap(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	fsm.SetAppliedIndexStore(&memAppliedIndexStore{idx: 10})
	fsm.Register(CmdHostUpsert, func(Command, *hraft.Log) error { return nil })

	if res := fsm.Apply(&hraft.Log{Index: 500, Data: mustCommand(t, CmdHostUpsert)}); res != nil {
		t.Fatalf("apply after gap: %v", res)
	}
	if got := fsm.DurableIndex(); got != 500 {
		t.Fatalf("DurableIndex=%d after a snapshot gap, want 500", got)
	}
}

// A transient failure (a statement timeout, a locked database) is the node's
// problem, not the command's: retry it, and if it still will not go through,
// refuse to advance the watermark so the next restart re-applies it. Advancing
// silently is what left one node permanently disagreeing with its peers about
// which host rows exist.
func TestFSM_TransientFailureRetriesAndFreezesWatermark(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	store := &memAppliedIndexStore{}
	fsm.SetAppliedIndexStore(store)

	calls := 0
	fsm.Register(CmdHostDelete, func(Command, *hraft.Log) error {
		calls++
		return fmt.Errorf("cascade delete host 5: %w", context.DeadlineExceeded)
	})

	res := fsm.Apply(&hraft.Log{Index: 42, Data: mustCommand(t, CmdHostDelete)})
	if res == nil {
		t.Fatal("a transient failure must surface as an error, not be swallowed")
	}
	if calls != applierRetryAttempts+1 {
		t.Fatalf("applier ran %d times, want %d (1 + retries)", calls, applierRetryAttempts+1)
	}
	if !fsm.Stalled() {
		t.Fatal("FSM should report itself stalled after an unrecoverable transient failure")
	}
	if got := fsm.DurableIndex(); got != 0 {
		t.Fatalf("DurableIndex=%d, want 0 — the watermark must not pass a lost write", got)
	}

	// Later entries keep being applied (the node stays useful) but the
	// watermark stays put, so the restart replays from the failure.
	fsm.Register(CmdHostUpsert, func(Command, *hraft.Log) error { return nil })
	if res := fsm.Apply(&hraft.Log{Index: 43, Data: mustCommand(t, CmdHostUpsert)}); res != nil {
		t.Fatalf("apply after stall: %v", res)
	}
	if got := fsm.DurableIndex(); got != 0 {
		t.Fatalf("DurableIndex=%d after a later apply, want 0 while stalled", got)
	}
}

// A transient failure that clears on retry is a non-event: the entry applies
// and the watermark advances.
func TestFSM_TransientFailureThatClearsOnRetry(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	fsm.SetAppliedIndexStore(&memAppliedIndexStore{})

	calls := 0
	fsm.Register(CmdHostDelete, func(Command, *hraft.Log) error {
		calls++
		if calls == 1 {
			return errors.New("database is locked")
		}
		return nil
	})

	if res := fsm.Apply(&hraft.Log{Index: 7, Data: mustCommand(t, CmdHostDelete)}); res != nil {
		t.Fatalf("apply: %v", res)
	}
	if calls != 2 {
		t.Fatalf("applier ran %d times, want 2 (one failure, one retry)", calls)
	}
	if fsm.Stalled() {
		t.Fatal("a retry that succeeded must not stall the FSM")
	}
	if got := fsm.DurableIndex(); got != 7 {
		t.Fatalf("DurableIndex=%d, want 7", got)
	}
}

// A deterministic failure fails identically on every replica, so the index
// legitimately advances — the cluster stays consistent about that record.
func TestFSM_DeterministicFailureAdvancesWatermark(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	fsm.SetAppliedIndexStore(&memAppliedIndexStore{})

	calls := 0
	fsm.Register(CmdHostUpsert, func(Command, *hraft.Log) error {
		calls++
		return errors.New("UNIQUE constraint failed: hosts.mac_address")
	})

	if res := fsm.Apply(&hraft.Log{Index: 9, Data: mustCommand(t, CmdHostUpsert)}); res == nil {
		t.Fatal("expected the applier error to surface")
	}
	if calls != 1 {
		t.Fatalf("applier ran %d times, want 1 — a deterministic failure must not be retried", calls)
	}
	if got := fsm.DurableIndex(); got != 9 {
		t.Fatalf("DurableIndex=%d, want 9", got)
	}
	if fsm.Stalled() {
		t.Fatal("a deterministic failure must not stall the FSM")
	}
}

func TestIsTransientApplyError(t *testing.T) {
	t.Parallel()
	transient := []error{
		context.DeadlineExceeded,
		fmt.Errorf("wrapped: %w", context.Canceled),
		errors.New("database is locked"),
		errors.New("SQLITE_BUSY: database is busy"),
		errors.New("read tcp 10.0.0.2:5432: i/o timeout"),
	}
	for _, err := range transient {
		if !isTransientApplyError(err) {
			t.Fatalf("%v should be transient", err)
		}
	}
	deterministic := []error{
		nil,
		ErrUnknownCommand,
		errors.New("UNIQUE constraint failed: hosts.name"),
		errors.New("decode command: unexpected end of JSON input"),
	}
	for _, err := range deterministic {
		if isTransientApplyError(err) {
			t.Fatalf("%v should NOT be transient", err)
		}
	}
}

// A snapshot moves raft's own lastApplied past the entry we failed to apply, so
// taking one while stalled would quietly cancel the restart's chance to retry.
func TestFSM_StalledFSMHoldsOffSnapshots(t *testing.T) {
	t.Parallel()
	fsm := NewFSM(newTestLogger(t))
	fsm.SetAppliedIndexStore(&memAppliedIndexStore{})
	fsm.Register(CmdHostDelete, func(Command, *hraft.Log) error {
		return fmt.Errorf("cascade delete host 5: %w", context.DeadlineExceeded)
	})
	if res := fsm.Apply(&hraft.Log{Index: 42, Data: mustCommand(t, CmdHostDelete)}); res == nil {
		t.Fatal("expected the transient failure to surface")
	}

	if _, err := fsm.Snapshot(); err == nil {
		t.Fatal("a stalled FSM must decline to snapshot while the hold lasts")
	}

	// Once the grace period is spent the hold is released, loudly, so a node
	// nobody restarts cannot grow its log for ever.
	fsm.flushMu.Lock()
	fsm.stalledAt = time.Now().Add(-stallSnapshotGrace - time.Minute)
	fsm.flushMu.Unlock()
	if _, err := fsm.Snapshot(); err != nil {
		t.Fatalf("snapshot after the grace period: %v", err)
	}
	if fsm.Stalled() {
		t.Fatal("the stall must be cleared when the hold is released")
	}
}
