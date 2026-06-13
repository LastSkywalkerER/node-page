package bridge

import (
	"testing"

	raftcluster "system-stats/internal/cluster/raft"
)

// Connector upsert/delete carry secrets encrypted under the LOCAL cluster's
// key and must NEVER cross the bridge — in either mode (push / both).
func TestShouldShipDeniesConnectorSecretsInEveryMode(t *testing.T) {
	for _, mode := range []struct {
		name       string
		uplinkOnly bool
	}{
		{"both", false},
		{"push", true},
	} {
		s := &Sender{myCluster: "cluster-a", uplinkOnly: mode.uplinkOnly}
		for _, ct := range []raftcluster.CommandType{
			raftcluster.CmdConnectorUpsert,
			raftcluster.CmdConnectorDelete,
		} {
			if s.shouldShip(raftcluster.Command{Type: ct, OriginClusterID: "cluster-a"}) {
				t.Errorf("mode=%s: command %d must never be shipped over the bridge", mode.name, ct)
			}
		}
	}
}

// CmdConnectorHostUpsert (discovered host topology, no secrets) is in the
// uplink allowlist and must still ship — the deny-list only covers the
// secret-bearing connector rows.
func TestShouldShipAllowsConnectorHostUpsert(t *testing.T) {
	s := &Sender{myCluster: "cluster-a", uplinkOnly: true}
	if !s.shouldShip(raftcluster.Command{Type: raftcluster.CmdConnectorHostUpsert, OriginClusterID: "cluster-a"}) {
		t.Fatal("connector-discovered host upsert (no secret) must still ship in push mode")
	}
}

// "both" mode ships everything except the deny-list / bookkeeping / peer-origin.
func TestShouldShipBothModeShipsConfigButNotSecrets(t *testing.T) {
	s := &Sender{myCluster: "cluster-a", uplinkOnly: false}
	if !s.shouldShip(raftcluster.Command{Type: raftcluster.CmdConfigSet, OriginClusterID: "cluster-a"}) {
		t.Fatal("config set should ship in both mode")
	}
	if s.shouldShip(raftcluster.Command{Type: raftcluster.CmdBridgeAck, OriginClusterID: "cluster-a"}) {
		t.Fatal("bridge bookkeeping must not ship")
	}
	// Peer-origin entries are never echoed back.
	if s.shouldShip(raftcluster.Command{Type: raftcluster.CmdMetricBatch, OriginClusterID: "cluster-b"}) {
		t.Fatal("peer-origin entries must not be re-shipped")
	}
}
