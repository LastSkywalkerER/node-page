package raft

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

// Follower→leader command forwarding (POST /raft/forward) is authenticated with
// an HMAC over the request body, keyed by the cluster-shared secret (the JWT
// secret every peer of a cluster already holds). Without it the endpoint would
// apply ANY Command from anyone who can reach the leader's HTTP port —
// CmdUserUpsert{Role:ADMIN}, CmdConfigSet, CmdGatewayRouteUpsert, … — a full
// cluster takeover. The signature proves the caller is a cluster peer.
//
// This is a leaf helper (no imports from sibling packages) so package raft can
// use it without an import cycle — the cross-cluster bridge keeps its own,
// separately-keyed HMAC in internal/cluster/raft/bridge.
//
// Same-cluster, HMAC-authenticated LAN channel: like the intra-cluster metric
// stream we verify only the signature, not a freshness window. A replay just
// re-submits an already-shared mutation the leader would apply idempotently or
// reject — it grants no capability the signature didn't already authorise — and
// dropping legitimate forwards over peer clock drift (SBCs/containers without
// NTP) would wedge writes cluster-wide.
// CapForwardHMAC is the peer_node_advertise capability a node publishes to
// announce it SIGNS its command forwards. The leader enforces signatures only
// once EVERY voter advertises it — so a rolling upgrade of an existing cluster
// (or a bridge hub/spoke) stays writable while a mix of old (unsigned) and new
// (signed) nodes coexist, then auto-enforces the moment the last old voter
// upgrades and advertises the capability. No manual flag, no permanent gap.
const CapForwardHMAC = "fwd-hmac"

const (
	// ForwardSignatureHeader carries hex(HMAC-SHA256(secret, ts || ':' || body)).
	ForwardSignatureHeader = "X-NS-Forward-Signature"
	// ForwardTimestampHeader carries the sender's unix-nanosecond timestamp,
	// folded into the signed payload so a signature can't be lifted onto a
	// different body.
	ForwardTimestampHeader = "X-NS-Forward-Timestamp"
	// ForwardClusterHeader carries the sender's cluster id; the leader rejects a
	// forward that does not name its own cluster.
	ForwardClusterHeader = "X-NS-Forward-Cluster"
)

// signForward produces the forwarding signature. Mirrors the bridge's Sign so
// the construction is identical, but keyed by the intra-cluster secret.
func signForward(secret string, tsNanos int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(tsNanos, 10)))
	_, _ = mac.Write([]byte{':'})
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyForward checks a forwarding signature in constant time.
func verifyForward(secret, header string, tsNanos int64, body []byte) error {
	if secret == "" {
		return errors.New("raft: forward secret not configured")
	}
	if header == "" {
		return errors.New("raft: missing forward signature")
	}
	expected := signForward(secret, tsNanos, body)
	if !hmac.Equal([]byte(expected), []byte(header)) {
		return errors.New("raft: forward signature mismatch")
	}
	return nil
}
