package raft

import (
	"context"
	"encoding/json"
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
