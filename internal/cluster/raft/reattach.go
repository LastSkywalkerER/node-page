package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// A node that peers can no longer dial cannot fix itself alone: the cluster's
// record of its address is a Raft membership change, and only the LEADER can
// make one. Its OUTBOUND paths still work though — that is how it keeps
// streaming metrics — so it asks a peer to make the change on its behalf.
//
// The request is signed with the cluster-shared secret, reusing the SAME
// scheme as follower→leader command forwarding (forwardauth.go) — it moves a
// voter's address, so an unsigned one would let anyone reachable on the app
// port redirect cluster traffic. A peer that is not the leader forwards it
// once to the leader's advertised URL; the hop header stops it bouncing on.
const (
	// ReattachRoutePath is relative to the /api/v1 router group.
	ReattachRoutePath = "/cluster/reattach"
	reattachPath      = "/api/v1" + ReattachRoutePath
	reattachHopHeader = "X-Raft-Reattach-Hop"
	reattachTimeout   = 5 * time.Second
)

// ReattachPayload is one node asking the cluster to reach it somewhere else.
type ReattachPayload struct {
	ClusterID string `json:"cluster_id"`
	NodeID    string `json:"node_id"`
	// RaftAddr is the host:port peers should dial for the Raft transport.
	RaftAddr string `json:"raft_addr"`
	// HTTPURL is where this node's API/dashboard answers, for the catalog.
	HTTPURL string `json:"http_url,omitempty"`
}

// Validate rejects a payload that could not possibly be applied, so a typo
// never becomes a membership change pointing at nothing.
func (p ReattachPayload) Validate() error {
	if strings.TrimSpace(p.ClusterID) == "" || strings.TrimSpace(p.NodeID) == "" {
		return fmt.Errorf("reattach: cluster id and node id are required")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(p.RaftAddr))
	if err != nil {
		return fmt.Errorf("reattach: raft address must be host:port, got %q", p.RaftAddr)
	}
	if host == "" {
		return fmt.Errorf("reattach: raft address needs a host, got %q", p.RaftAddr)
	}
	if n, cerr := strconv.Atoi(port); cerr != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("reattach: %q is not a valid port", port)
	}
	return nil
}

// AskPeersToReattach POSTs the signed request to this cluster's known peers
// and returns once one of them applied it. Peers are tried in catalog order;
// the first success wins, and every failure is collected so the caller can
// tell the operator what stood in the way.
func AskPeersToReattach(ctx context.Context, logger *log.Logger, db *gorm.DB, client *http.Client, secret string, p ReattachPayload) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if secret == "" {
		return fmt.Errorf("reattach: no cluster secret to sign the request with")
	}
	if db == nil {
		return fmt.Errorf("reattach: no peer catalog available")
	}
	urls, err := ListClusterPeerURLs(ctx, db, p.ClusterID, p.NodeID)
	if err != nil {
		return fmt.Errorf("reattach: read peer catalog: %w", err)
	}
	if len(urls) == 0 {
		return fmt.Errorf("reattach: this node knows no peer to ask — no other node has published its URL yet")
	}
	if client == nil {
		client = &http.Client{Timeout: reattachTimeout}
	}
	var failures []string
	for _, u := range urls {
		if err := postReattach(ctx, client, u, secret, p, 0); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", u, err))
			continue
		}
		if logger != nil {
			logger.Info("raft: cluster accepted this node's new address", "peer", u, "addr", p.RaftAddr)
		}
		return nil
	}
	return fmt.Errorf("reattach: no peer could apply the change (%s)", strings.Join(failures, "; "))
}

func postReattach(ctx context.Context, client *http.Client, baseURL, secret string, p ReattachPayload, hop int) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, reattachTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+reattachPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := time.Now().UnixNano()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ForwardTimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(ForwardSignatureHeader, signForward(secret, ts, body))
	req.Header.Set(reattachHopHeader, strconv.Itoa(hop))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("status %d %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// ClusterReattach applies another node's new address on its behalf. Public
// route, HMAC-authenticated with the cluster-shared secret.
//
// POST /api/v1/cluster/reattach
func (h *Handler) ClusterReattach(c *gin.Context) {
	if h.svc == nil || !h.svc.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "raft disabled"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}
	secret := ""
	if h.forwardSecret != nil {
		secret = h.forwardSecret()
	}
	if secret == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no cluster secret configured"})
		return
	}
	ts, _ := strconv.ParseInt(c.GetHeader(ForwardTimestampHeader), 10, 64)
	if verr := verifyForward(secret, c.GetHeader(ForwardSignatureHeader), ts, body); verr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": verr.Error()})
		return
	}

	var p ReattachPayload
	if jerr := json.Unmarshal(body, &p); jerr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode: " + jerr.Error()})
		return
	}
	if verr := p.Validate(); verr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": verr.Error()})
		return
	}
	st := h.svc.Status()
	if p.ClusterID != st.ClusterID {
		c.JSON(http.StatusForbidden, gin.H{"error": "reattach: different cluster"})
		return
	}
	if p.NodeID == st.NodeID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reattach: a node cannot ask itself"})
		return
	}

	if !h.svc.IsLeader() {
		hop, _ := strconv.Atoi(c.GetHeader(reattachHopHeader))
		if hop > 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "reattach: not the leader (already forwarded once)"})
			return
		}
		leaderURL := h.leaderURL(c.Request.Context(), st)
		if leaderURL == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "reattach: this node is not the leader and does not know the leader's URL"})
			return
		}
		if ferr := postReattach(c.Request.Context(), &http.Client{Timeout: reattachTimeout}, leaderURL, secret, p, 1); ferr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "reattach: forwarding to the leader failed: " + ferr.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"applied": p.NodeID, "via": "leader"})
		return
	}

	// Leader: point the membership at the new address. hashicorp/raft updates
	// an existing server id in place, so this is not a re-join and no data is
	// touched. The catalog row follows so peers reach its HTTP surface too.
	if err := h.svc.AddVoter(p.NodeID, p.RaftAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reattach: " + err.Error()})
		return
	}
	if h.replicator != nil && p.HTTPURL != "" {
		if aerr := h.replicator.SubmitPeerNodeAdvertise(c.Request.Context(), p.ClusterID, p.NodeID, p.HTTPURL, []string{CapForwardHMAC}); aerr != nil && h.logger != nil {
			h.logger.Warn("raft: reattach published address but not the URL", "node_id", p.NodeID, "error", aerr)
		}
	}
	if h.logger != nil {
		h.logger.Info("raft: applied a node's new address on its request", "node_id", p.NodeID, "addr", p.RaftAddr)
	}
	c.JSON(http.StatusOK, gin.H{"applied": p.NodeID, "addr": p.RaftAddr})
}

// leaderURL resolves the current leader's advertised HTTP URL from the
// replicated catalog; "" when unknown.
func (h *Handler) leaderURL(ctx context.Context, st Status) string {
	if h.db == nil || st.LeaderID == "" || st.LeaderID == st.NodeID {
		return ""
	}
	url, err := LookupPeerURL(ctx, h.db, st.ClusterID, st.LeaderID)
	if err != nil {
		return ""
	}
	return strings.TrimRight(url, "/")
}
