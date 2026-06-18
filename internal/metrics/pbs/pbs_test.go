package pbs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildBackupHealth(t *testing.T) {
	tasks := []Task{
		{Type: "backup", StartTime: 300, EndTime: 350, Status: "OK"},
		{Type: "verify", StartTime: 250, EndTime: 260, Status: "verification failed"},
		{Type: "backup", StartTime: 200, EndTime: 0, Status: ""}, // running
		{Type: "reader", StartTime: 280, EndTime: 290, Status: "OK"}, // irrelevant type
	}
	h := buildBackupHealth(tasks)

	if h.LastBackupAt != 300 {
		t.Errorf("LastBackupAt = %d, want 300", h.LastBackupAt)
	}
	if h.LastErrorAt != 250 || h.LastError != "verification failed" {
		t.Errorf("last error = %d/%q, want 250/verification failed", h.LastErrorAt, h.LastError)
	}
	if h.RunningTasks != 1 {
		t.Errorf("RunningTasks = %d, want 1", h.RunningTasks)
	}
	if len(h.RecentTasks) != 3 { // reader excluded
		t.Fatalf("RecentTasks = %d, want 3 (reader excluded)", len(h.RecentTasks))
	}
	// Newest first; the running task carries the "running" state.
	if h.RecentTasks[2].Status != "running" {
		t.Errorf("oldest recent task status = %q, want running", h.RecentTasks[2].Status)
	}
}

func TestBuildBackupHealth_CapsRecent(t *testing.T) {
	var tasks []Task
	for i := 0; i < 20; i++ {
		tasks = append(tasks, Task{Type: "backup", StartTime: int64(i), EndTime: int64(i) + 1, Status: "OK"})
	}
	if got := len(buildBackupHealth(tasks).RecentTasks); got != recentTaskCap {
		t.Fatalf("RecentTasks = %d, want cap %d", got, recentTaskCap)
	}
}

func TestPBSHostName(t *testing.T) {
	cases := []struct{ node, host, want string }{
		{"localhost", "pbs.lan:8007", "pbs.lan"},
		{"localhost", "10.0.0.5:8007", "10.0.0.5"},
		{"backup01", "10.0.0.5:8007", "backup01"},
	}
	for _, c := range cases {
		if got := pbsHostName(c.node, c.host); got != c.want {
			t.Errorf("pbsHostName(%q,%q) = %q, want %q", c.node, c.host, got, c.want)
		}
	}
}

func TestIPFromHost(t *testing.T) {
	if got := ipFromHost("10.0.0.5:8007"); got != "10.0.0.5" {
		t.Errorf("ipFromHost(IP) = %q, want 10.0.0.5", got)
	}
	if got := ipFromHost("pbs.lan:8007"); got != "" {
		t.Errorf("ipFromHost(hostname) = %q, want empty", got)
	}
}

func TestClientAuthHeaderAndParsing(t *testing.T) {
	const tokenID = "monitor@pbs!stats"
	const secret = "deadbeef-uuid"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api2/json/status/datastore-usage":
			_, _ = w.Write([]byte(`{"data":[{"store":"main","total":1000,"used":250,"avail":750}]}`))
		case "/api2/json/nodes/localhost/status":
			_, _ = w.Write([]byte(`{"data":{"uptime":3600,"cpu":0.1,"kversion":"Linux 6.8","memory":{"total":100,"used":40,"free":60},"root":{"total":500,"used":100,"avail":400},"cpuinfo":{"model":"Xeon","cpus":8}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, tokenID, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	stores, err := client.DatastoreUsage(ctx)
	if err != nil {
		t.Fatalf("DatastoreUsage: %v", err)
	}
	// PBS uses ':' between token id and secret (PVE uses '=').
	if want := "PBSAPIToken=" + tokenID + ":" + secret; gotAuth != want {
		t.Errorf("auth header = %q, want %q", gotAuth, want)
	}
	if len(stores) != 1 || stores[0].Store != "main" || stores[0].Used != 250 {
		t.Fatalf("datastores = %+v", stores)
	}

	st, err := client.NodeStatus(ctx, "localhost")
	if err != nil {
		t.Fatalf("NodeStatus: %v", err)
	}
	if st.Root.Total != 500 || st.Root.Used != 100 || st.CPUInfo.Model != "Xeon" || st.CPUInfo.CPUs != 8 {
		t.Fatalf("node status parsed wrong: %+v", st)
	}
}

func TestDatastoreGroupsParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/admin/datastore/main/groups" {
			// PBS uses kebab-case keys — the struct tags must match exactly.
			_, _ = w.Write([]byte(`{"data":[
				{"backup-type":"vm","backup-id":"100","last-backup":1700000000,"backup-count":12},
				{"backup-type":"ct","backup-id":"201","last-backup":1700009999,"backup-count":3}
			]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "t@pbs!x", "s", false)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := client.DatastoreGroups(context.Background(), "main")
	if err != nil {
		t.Fatalf("DatastoreGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].BackupType != "vm" || groups[0].BackupID != "100" || groups[0].LastBackup != 1700000000 || groups[0].BackupCount != 12 {
		t.Errorf("group[0] parsed wrong: %+v", groups[0])
	}
}

func TestDatastoreSnapshotsParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/admin/datastore/main/snapshots" {
			_, _ = w.Write([]byte(`{"data":[
				{"backup-type":"vm","backup-id":"100","backup-time":1700000000,"size":1048576,"comment":"old-name"},
				{"backup-type":"vm","backup-id":"100","backup-time":1700086400,"size":2097152,"comment":"dokploy"}
			]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "t@pbs!x", "s", false)
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := client.DatastoreSnapshots(context.Background(), "main")
	if err != nil {
		t.Fatalf("DatastoreSnapshots: %v", err)
	}
	if len(snaps) != 2 || snaps[1].Size != 2097152 || snaps[1].BackupTime != 1700086400 || snaps[1].Comment != "dokploy" {
		t.Fatalf("snapshots parsed wrong: %+v", snaps)
	}
}

func TestLatestSnapshotInfo(t *testing.T) {
	snaps := []Snapshot{
		{BackupType: "vm", BackupID: "100", BackupTime: 100, Size: 10, Comment: "old"},
		{BackupType: "vm", BackupID: "100", BackupTime: 300, Size: 30, Comment: "dokploy\nmore notes"}, // newest wins
		{BackupType: "vm", BackupID: "100", BackupTime: 200, Size: 20, Comment: "mid"},
		{BackupType: "ct", BackupID: "201", BackupTime: 50, Size: 5}, // no comment → no name
	}
	got := latestSnapshotInfo(snaps)
	if got["vm/100"].size != 30 {
		t.Errorf("vm/100 size = %d, want 30 (newest snapshot)", got["vm/100"].size)
	}
	if got["vm/100"].name != "dokploy" { // first line of the newest snapshot's notes
		t.Errorf("vm/100 name = %q, want dokploy", got["vm/100"].name)
	}
	if got["ct/201"].size != 5 || got["ct/201"].name != "" {
		t.Errorf("ct/201 = %+v, want size 5 / empty name", got["ct/201"])
	}
}
