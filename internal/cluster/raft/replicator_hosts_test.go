package raft

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	hosts "system-stats/internal/cluster/hosts"
)

// The backfill republishes table rows as CmdHostUpsert / CmdConnectorHostUpsert;
// it must carry each row's boot_time. It used to omit it, so every activation
// and every bridge reconcile (10-min cadence) zeroed the field on peers and on
// the hub — whose cards then fell back to the SERVER's process uptime, the same
// number on every remote machine.
func TestBackfillLocalHosts_CarriesBootTime(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&hosts.Host{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now()
	rows := []hosts.Host{
		{ID: hosts.LocalCollectorHostID, Name: "local", MacAddress: "00:00:00:00:00:01", Source: hosts.SourceAgent, BootTime: 1_700_000_001, LastSeen: now},
		{Name: "nas", MacAddress: "aa:aa:aa:aa:aa:02", Source: hosts.SourceAgent, BootTime: 1_700_000_002, LastSeen: now},
		{Name: "pve", MacAddress: "aa:aa:aa:aa:aa:03", Source: hosts.SourceConnector, HostType: hosts.HostTypeHypervisor, ExternalID: "pve:fp/node/pve", GuestStatus: "online", BootTime: 1_700_000_003, LastSeen: now},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", rows[i].Name, err)
		}
	}

	svc := &fakeReplSvc{}
	n, err := NewReplicator(svc).BackfillLocalHosts(context.Background(), hosts.NewRepository(db))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 3 {
		t.Fatalf("submitted = %d rows, want 3", n)
	}

	got := map[string]int64{}
	for _, cmd := range svc.cmds {
		switch cmd.Type {
		case CmdHostUpsert:
			var p HostUpsertPayload
			if err := json.Unmarshal(cmd.Payload, &p); err != nil {
				t.Fatalf("decode host payload: %v", err)
			}
			got[p.MacAddress] = p.BootTime
		case CmdConnectorHostUpsert:
			var p ConnectorHostUpsertPayload
			if err := json.Unmarshal(cmd.Payload, &p); err != nil {
				t.Fatalf("decode connector host payload: %v", err)
			}
			got[p.MacAddress] = p.BootTime
		default:
			t.Fatalf("unexpected command %q", cmd.Type)
		}
	}
	for _, r := range rows {
		bt, ok := got[r.MacAddress]
		if !ok {
			t.Fatalf("row %s (%s) was not republished", r.Name, r.MacAddress)
		}
		if bt != r.BootTime {
			t.Errorf("row %s republished with boot_time %d, want %d", r.Name, bt, r.BootTime)
		}
	}
}
