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

// A stale catalog row can carry THIS node's URL under an old cluster id
// (cluster rename) — the picker must never probe or pick it, and the hub
// seed must win instead.
func TestPickerExcludesOwnURL(t *testing.T) {
	db := pickerTestDB(t)
	own := pingServer(t)  // this node — fast, would win on RTT
	hub := pingServer(t)  // the real uplink target

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
