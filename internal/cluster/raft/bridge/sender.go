package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"

	raftcluster "system-stats/internal/cluster/raft"
)

// Envelope is the wire shape of a single replicated entry. It's a compact
// projection of raftcluster.ApplyEvent so the receiver can reconstruct a
// raftcluster.Command and submit it locally.
type Envelope struct {
	OriginIndex     uint64                  `json:"origin_index"`
	OriginClusterID string                  `json:"origin_cluster_id"`
	OriginNodeID    string                  `json:"origin_node_id"`
	Timestamp       time.Time               `json:"ts"`
	Type            raftcluster.CommandType `json:"type"`
	Payload         []byte                  `json:"payload"`
}

// Batch is the wire shape of one /raft/bridge/replicate POST body.
type Batch struct {
	Entries []Envelope `json:"entries"`
}

// Sender ships locally-applied FSM entries to the peer cluster's leader.
// It only ships when this node currently holds Raft leadership; followers
// drain the apply-event channel and discard.
type Sender struct {
	logger    *log.Logger
	svc       raftcluster.Service
	events    <-chan raftcluster.ApplyEvent
	picker    *Picker
	secret    string
	myCluster string
	myNode    string

	flushEvery time.Duration
	flushSize  int

	httpClient *http.Client
}

// NewSender wires the sender.
func NewSender(logger *log.Logger, svc raftcluster.Service, events <-chan raftcluster.ApplyEvent, picker *Picker, secret, myClusterID, myNodeID string) *Sender {
	return &Sender{
		logger:     logger,
		svc:        svc,
		events:     events,
		picker:     picker,
		secret:     secret,
		myCluster:  myClusterID,
		myNode:     myNodeID,
		flushEvery: 250 * time.Millisecond,
		flushSize:  50,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Run drains the apply-event channel and ships batches to the peer
// cluster's leader (chosen by the URL Picker). Stops when ctx is done.
func (s *Sender) Run(ctx context.Context) {
	tick := time.NewTicker(s.flushEvery)
	defer tick.Stop()
	var buf []Envelope

	flush := func() {
		if len(buf) == 0 {
			return
		}
		// Only the leader ships. Followers drained their buffer earlier
		// but we re-check here in case state changed mid-tick.
		if !s.svc.IsLeader() {
			buf = buf[:0]
			return
		}
		if err := s.shipBatch(ctx, Batch{Entries: buf}); err != nil {
			if s.logger != nil {
				s.logger.Warn("bridge: ship batch failed", "error", err, "entries", len(buf))
			}
			// Keep the buffer so the next tick retries. Cap so we don't
			// grow unbounded under sustained failure — drop oldest.
			if len(buf) > s.flushSize*4 {
				buf = buf[len(buf)-s.flushSize*4:]
			}
			return
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.events:
			// Skip entries that originated in a peer cluster — those are
			// the ones we just received over the bridge, never ship back.
			if ev.Cmd.OriginClusterID != "" && ev.Cmd.OriginClusterID != s.myCluster {
				continue
			}
			// Skip bridge bookkeeping entries; the peer doesn't need them.
			if ev.Cmd.Type == raftcluster.CmdBridgeAck {
				continue
			}
			env := Envelope{
				OriginIndex:     ev.Index,
				OriginClusterID: firstNonEmpty(ev.Cmd.OriginClusterID, s.myCluster),
				OriginNodeID:    firstNonEmpty(ev.Cmd.OriginNodeID, s.myNode),
				Timestamp:       ev.Cmd.Timestamp,
				Type:            ev.Cmd.Type,
				Payload:         ev.Cmd.Payload,
			}
			buf = append(buf, env)
			if len(buf) >= s.flushSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (s *Sender) shipBatch(ctx context.Context, batch Batch) error {
	if s.picker == nil {
		return errors.New("bridge: no picker configured")
	}
	url := s.picker.Pick("")
	if url == "" {
		return errors.New("bridge: no healthy peer URL")
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("encode batch: %w", err)
	}
	tsNanos := time.Now().UnixNano()
	sig := Sign(s.secret, tsNanos, body)

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url+"/api/v1/raft/bridge/replicate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HMACHeader, sig)
	req.Header.Set(TimestampHeader, strconv.FormatInt(tsNanos, 10))
	req.Header.Set(SenderClusterHeader, s.myCluster)
	req.Header.Set(SenderNodeHeader, s.myNode)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("peer returned %s", resp.Status)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
