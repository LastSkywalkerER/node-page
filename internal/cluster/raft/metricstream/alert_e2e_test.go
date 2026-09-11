package metricstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"system-stats/internal/app/config"
	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
)

// TestNodeAlertEndToEnd walks the whole path the SkyNAS case needs, with real
// components and a real HTTP hop: an isolated node's sender stamps its
// self-diagnosis onto the batch → the receiver authenticates it → the metric
// sink stores it against the LOCAL host row → the hosts service serves it on
// the host payload the machine cards read. Then the node recovers and the next
// batch clears the alert everywhere.
//
// This is the wire contract between versions, so it is asserted on the JSON a
// browser would actually receive, not on Go structs.
func TestNodeAlertEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "cluster-shared-jwt-secret"
	const skynasMAC = "02:42:ac:1b:00:03"

	// Receiving node (a peer / the bridged hub): DB + host rows + the real
	// sink, alert store and hosts service wired the way DI wires them.
	// A FILE-backed sqlite, not ":memory:": the HTTP handler runs on another
	// goroutine and each pooled connection to an in-memory sqlite gets its own
	// empty database, so the receiver would see no tables at all.
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "e2e.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&hosts.Host{}, &peerNodeAdvertiseRow{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := hosts.NewRepository(db)
	now := time.Now()
	for _, h := range []hosts.Host{
		{ID: hosts.LocalCollectorHostID, Name: "orangepi5-plus", MacAddress: "02:42:0a:00:0c:03", LastSeen: now, CreatedAt: now, UpdatedAt: now},
		{ID: 9, Name: "SkyNAS", MacAddress: skynasMAC, IPv4: "192.168.0.110", LastSeen: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&h).Error; err != nil {
			t.Fatalf("seed host: %v", err)
		}
	}
	store := hosts.NewNodeAlertStore()
	sink := raftcluster.NewMetricSink(raftcluster.AppliersDeps{
		Logger:   log.New(io.Discard),
		HostRepo: repo,
	})
	sink.SetNodeAlertStore(store)
	hostSvc := hosts.NewService(log.New(io.Discard), repo)
	hosts.AttachNodeAlertSource(hostSvc, store)

	receiver := NewReceiver(log.New(io.Discard), sink, "sky-home", secret, config.RaftBridgeConfig{})
	router := gin.New()
	var lastStatus, delivered atomic.Int32
	router.POST("/api/v1"+RoutePath, func(c *gin.Context) {
		receiver.Handle(c)
		lastStatus.Store(int32(c.Writer.Status()))
		delivered.Add(1)
	})
	peer := httptest.NewServer(router)
	defer peer.Close()

	// Sending node (SkyNAS): the real sender, shipping to that peer.
	if err := db.Create(&peerNodeAdvertiseRow{ClusterID: "sky-home", NodeID: "orangepi5-plus", URL: peer.URL}).Error; err != nil {
		t.Fatalf("seed peer catalog: %v", err)
	}
	sender := NewSender(log.New(io.Discard), db, "sky-home", "skynas", secret, config.RaftBridgeConfig{})
	var diagnosis *hosts.NodeAlert
	sender.SetNodeAlertSource(func() *hosts.NodeAlert { return diagnosis })

	// alertOf reads the machine card's own source of truth: the host payload.
	alertOf := func(id uint) map[string]any {
		t.Helper()
		list, lerr := hostSvc.GetAllHosts(context.Background())
		if lerr != nil {
			t.Fatalf("GetAllHosts: %v", lerr)
		}
		raw, merr := json.Marshal(map[string]any{"hosts": list})
		if merr != nil {
			t.Fatalf("marshal: %v", merr)
		}
		var payload struct {
			Hosts []struct {
				ID        uint           `json:"id"`
				NodeAlert map[string]any `json:"node_alert"`
			} `json:"hosts"`
		}
		if uerr := json.Unmarshal(raw, &payload); uerr != nil {
			t.Fatalf("unmarshal: %v", uerr)
		}
		for _, h := range payload.Hosts {
			if h.ID == id {
				return h.NodeAlert
			}
		}
		t.Fatalf("host %d missing from the payload", id)
		return nil
	}

	ship := func() {
		t.Helper()
		want := delivered.Load() + 1
		sender.lastShipAt.Store(0) // this test drives the cadence explicitly
		sender.Broadcast(context.Background(), raftcluster.MetricBatchPayload{
			HostMAC: skynasMAC, HostName: "SkyNAS", Timestamp: time.Now().UTC(),
		})
		// Broadcast is fire-and-forget per target; wait for the POST to land.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if delivered.Load() >= want {
				if got := lastStatus.Load(); got != 200 {
					t.Fatalf("peer rejected the batch: status %d", got)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("batch never reached the peer")
	}

	// Healthy node: no alert anywhere.
	ship()
	if a := alertOf(9); a != nil {
		t.Fatalf("healthy node produced an alert on the host payload: %v", a)
	}

	// The node diagnoses itself as cut off and keeps streaming metrics.
	diagnosis = &hosts.NodeAlert{
		Kind:          hosts.NodeAlertRaftIsolated,
		Severity:      hosts.NodeAlertSeverityError,
		Title:         "Node advertises an address it no longer has",
		Detail:        "peers keep dialing 192.168.0.110:7000",
		Steps:         []string{"give it 192.168.0.110 back", "or re-advertise 192.168.0.103:7000"},
		NodeID:        "skynas",
		AdvertiseAddr: "192.168.0.110:7000",
		LocalIPv4:     "192.168.0.103",
		Since:         time.Now().Add(-20 * time.Hour).UTC(),
	}
	ship()

	a := alertOf(9)
	if a == nil {
		t.Fatal("the isolated node's alert never reached the host payload")
	}
	if a["kind"] != hosts.NodeAlertRaftIsolated || a["node_id"] != "skynas" || a["advertise_addr"] != "192.168.0.110:7000" || a["local_ipv4"] != "192.168.0.103" {
		t.Fatalf("alert facts lost on the wire: %v", a)
	}
	steps, ok := a["steps"].([]any)
	if !ok || len(steps) != 2 || !strings.Contains(steps[1].(string), "192.168.0.103:7000") {
		t.Fatalf("re-attach steps lost on the wire: %v", a["steps"])
	}
	// Only the machine that reported it is marked — not every card.
	if other := alertOf(hosts.LocalCollectorHostID); other != nil {
		t.Fatalf("an unrelated host was marked: %v", other)
	}

	// Operator re-attached it: the next batch carries no alert and the card
	// goes clean immediately, without waiting out the TTL.
	diagnosis = nil
	ship()
	if a := alertOf(9); a != nil {
		t.Fatalf("alert still served after the node recovered: %v", a)
	}
}

// peerNodeAdvertiseRow mirrors the raft package's unexported catalog row so the
// sender's peer discovery can be seeded from this test.
type peerNodeAdvertiseRow struct {
	ClusterID    string    `gorm:"primaryKey;size:64"`
	NodeID       string    `gorm:"primaryKey;size:64"`
	URL          string    `gorm:"type:text"`
	Capabilities string    `gorm:"type:text"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

func (peerNodeAdvertiseRow) TableName() string { return "peer_node_advertise" }
