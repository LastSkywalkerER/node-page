package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func pickerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE peer_node_advertise (
		cluster_id TEXT, node_id TEXT, url TEXT, capabilities TEXT, updated_at DATETIME,
		PRIMARY KEY (cluster_id, node_id))`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func pingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pingServerWithID mimics a node whose /raft/ping reports a live cluster/node
// id via the X-Raft-* headers (as the real handler does).
func pingServerWithID(t *testing.T, clusterID, nodeID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Raft-Cluster-ID", clusterID)
		w.Header().Set("X-Raft-Node-ID", nodeID)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A catalog row can carry a SAME-cluster peer under a stale cluster id (e.g. the
// leader stamped it under the pre-rename name at join time). The picker must
// trust the live cluster id the node reports over /raft/ping, recognise it as
// our own cluster, and never pick it as an uplink target — otherwise it ships
// replication batches at a sibling node, which refuses them (404 "bridge
// receiving is not enabled").
func TestPickerExcludesSameClusterViaLivePing(t *testing.T) {
	db := pickerTestDB(t)
	// Sibling node, fast (would win on RTT), but its catalog row is stale.
	sibling := pingServerWithID(t, "sky-home", "orangepi5-plus")
	hub := pingServerWithID(t, "hub-cluster", "hub")

	if err := db.Exec(`INSERT INTO peer_node_advertise (cluster_id, node_id, url) VALUES (?, ?, ?)`,
		"old-name-ef5fcc", "orangepi5-plus", sibling.URL).Error; err != nil {
		t.Fatal(err)
	}

	p := NewPicker(nil, db, "sky-home", []string{hub.URL}, "")
	p.probeOnce(context.Background())

	if got := p.Pick(""); got != hub.URL {
		t.Fatalf("Pick() = %q, want hub %q (same-cluster sibling must be excluded via live ping id)", got, hub.URL)
	}
}

// A stale catalog row can carry THIS node's URL under an old cluster id
// (cluster rename) — the picker must never probe or pick it, and the hub
// seed must win instead.
func TestPickerExcludesOwnURL(t *testing.T) {
	db := pickerTestDB(t)
	own := pingServer(t) // this node — fast, would win on RTT
	hub := pingServer(t) // the real uplink target

	// Own URL advertised under the pre-rename cluster id.
	if err := db.Exec(`INSERT INTO peer_node_advertise (cluster_id, node_id, url) VALUES (?, ?, ?)`,
		"local", "dokploy", own.URL).Error; err != nil {
		t.Fatal(err)
	}

	p := NewPicker(nil, db, "site-abc123", []string{hub.URL + "/"}, own.URL)
	p.probeOnce(context.Background())

	if got := p.Pick(""); got != hub.URL {
		t.Fatalf("Pick() = %q, want hub %q (own URL must be excluded)", got, hub.URL)
	}
	for _, s := range p.Snapshot() {
		if s.URL == own.URL {
			t.Fatalf("own URL %q present in snapshot", own.URL)
		}
	}
}

// Measurements must follow the probe list: an entry removed from the
// catalog (or newly filtered out) may not survive as a frozen "healthy"
// sample that keeps winning Pick().
func TestPickerPrunesStaleMeasures(t *testing.T) {
	db := pickerTestDB(t)
	peer := pingServer(t)
	hub := pingServer(t)

	if err := db.Exec(`INSERT INTO peer_node_advertise (cluster_id, node_id, url) VALUES (?, ?, ?)`,
		"other-site", "n1", peer.URL).Error; err != nil {
		t.Fatal(err)
	}

	p := NewPicker(nil, db, "site-abc123", []string{hub.URL}, "")
	p.probeOnce(context.Background())
	if len(p.Snapshot()) != 2 {
		t.Fatalf("expected 2 samples, got %+v", p.Snapshot())
	}

	// The catalog row goes away (peer removed / renamed elsewhere).
	if err := db.Exec(`DELETE FROM peer_node_advertise`).Error; err != nil {
		t.Fatal(err)
	}
	p.probeOnce(context.Background())

	samples := p.Snapshot()
	if len(samples) != 1 || samples[0].URL != hub.URL {
		t.Fatalf("stale measure survived catalog removal: %+v", samples)
	}
}
