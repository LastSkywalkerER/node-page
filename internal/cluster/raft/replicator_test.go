package raft

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	connectors "system-stats/internal/platform/connectors"
)

// fakeReplSvc records every Command submitted so the backfill helpers can be
// asserted without a real Raft node. Always reports the layer as enabled.
type fakeReplSvc struct {
	cmds []Command
}

func (f *fakeReplSvc) SubmitCommand(_ context.Context, cmd Command, _ time.Duration) (SubmitResult, error) {
	f.cmds = append(f.cmds, cmd)
	return SubmitResult{Applied: true}, nil
}
func (f *fakeReplSvc) Status() Status                   { return Status{Enabled: true} }
func (f *fakeReplSvc) Enabled() bool                    { return true }
func (f *fakeReplSvc) IsLeader() bool                   { return true }
func (f *fakeReplSvc) AddVoter(string, string) error    { return nil }
func (f *fakeReplSvc) AddNonvoter(string, string) error { return nil }
func (f *fakeReplSvc) DemoteVoter(string) error         { return nil }
func (f *fakeReplSvc) RemovePeer(string) error          { return nil }
func (f *fakeReplSvc) TransferLeadership() error        { return nil }
func (f *fakeReplSvc) Stats() map[string]string         { return nil }
func (f *fakeReplSvc) Close() error                     { return nil }

// stubConnRepo is a minimal connectors.Repository that only serves List.
type stubConnRepo struct {
	rows []connectors.Connector
	err  error
}

func (r *stubConnRepo) List(context.Context) ([]connectors.Connector, error) { return r.rows, r.err }
func (r *stubConnRepo) GetByID(context.Context, uint) (*connectors.Connector, error) {
	return nil, nil
}
func (r *stubConnRepo) GetByFingerprint(context.Context, string) (*connectors.Connector, error) {
	return nil, nil
}
func (r *stubConnRepo) Upsert(context.Context, *connectors.Connector) error { return nil }
func (r *stubConnRepo) DeleteByFingerprint(context.Context, string) error   { return nil }
func (r *stubConnRepo) UpdateLocalStatus(context.Context, uint, string, string, time.Time) error {
	return nil
}

func TestBackfillLocalConnectors_RepublishesEachRow(t *testing.T) {
	svc := &fakeReplSvc{}
	repl := NewReplicator(svc)
	repo := &stubConnRepo{rows: []connectors.Connector{
		{Type: connectors.TypeProxmox, Fingerprint: "node/inno-pve@192.168.0.87:8006", Endpoint: "https://192.168.0.87:8006", SecretEnc: []byte("enc")},
		{Type: connectors.TypePBS, Fingerprint: "pbs/backup", Endpoint: "https://backup:8007"},
		{Type: connectors.TypeProxmox, Fingerprint: ""}, // skipped: no fingerprint
	}}

	n, err := repl.BackfillLocalConnectors(context.Background(), repo)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 2 {
		t.Fatalf("submitted = %d, want 2 (the empty-fingerprint row is skipped)", n)
	}
	if len(svc.cmds) != 2 {
		t.Fatalf("submitted %d commands, want 2", len(svc.cmds))
	}
	for _, cmd := range svc.cmds {
		if cmd.Type != CmdConnectorUpsert {
			t.Errorf("cmd type = %q, want %q", cmd.Type, CmdConnectorUpsert)
		}
		var p ConnectorUpsertPayload
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Fingerprint == "" {
			t.Error("an empty-fingerprint connector was submitted")
		}
	}
}

func TestBackfillLocalConnectors_DisabledIsNoop(t *testing.T) {
	repl := NewReplicator(DisabledService{})
	repo := &stubConnRepo{rows: []connectors.Connector{{Fingerprint: "x"}}}
	n, err := repl.BackfillLocalConnectors(context.Background(), repo)
	if err != nil || n != 0 {
		t.Fatalf("disabled backfill should be a no-op, got n=%d err=%v", n, err)
	}
}
