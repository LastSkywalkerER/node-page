package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	raftcluster "system-stats/internal/cluster/raft"
)

// fakeService is a minimal raftcluster.Service for receiver tests. It counts
// SubmitCommand calls so a test can assert the all-deduped path never submits.
type fakeService struct {
	leader    bool
	submitCnt int64
}

func (f *fakeService) SubmitCommand(_ context.Context, _ raftcluster.Command, _ time.Duration) (raftcluster.SubmitResult, error) {
	atomic.AddInt64(&f.submitCnt, 1)
	return raftcluster.SubmitResult{Index: 1, Applied: true}, nil
}
func (f *fakeService) Status() raftcluster.Status       { return raftcluster.Status{Enabled: true} }
func (f *fakeService) Enabled() bool                    { return true }
func (f *fakeService) IsLeader() bool                   { return f.leader }
func (f *fakeService) AddVoter(string, string) error    { return nil }
func (f *fakeService) AddNonvoter(string, string) error { return nil }
func (f *fakeService) DemoteVoter(string) error         { return nil }
func (f *fakeService) RemovePeer(string) error          { return nil }
func (f *fakeService) TransferLeadership() error        { return nil }
func (f *fakeService) Stats() map[string]string         { return nil }
func (f *fakeService) Close() error                     { return nil }

func postReplicate(t *testing.T, rec *Receiver, secret, senderCluster string, batch Batch) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	tsNanos := time.Now().UnixNano()
	sig := Sign(secret, tsNanos, body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/raft/bridge/replicate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HMACHeader, sig)
	req.Header.Set(TimestampHeader, strconv.FormatInt(tsNanos, 10))
	req.Header.Set(SenderClusterHeader, senderCluster)
	req.Header.Set(SenderNodeHeader, "remote-node")
	c.Request = req

	rec.Handle(c)
	return w
}

// When every entry in the batch was already applied, the receiver must answer
// 200 immediately (idempotent success) and never call SubmitCommand — never 500.
func TestReceiverAllDedupedReturns200NoSubmit(t *testing.T) {
	db := newTestDB(t)
	secret := "shared-secret"
	const remoteCluster = "remote"

	// Pre-mark indexes 1,2,3 from the remote cluster as already applied.
	if err := MarkAppliedBatch(context.Background(), db, []AppliedRemoteLog{
		{OriginClusterID: remoteCluster, OriginIndex: 1},
		{OriginClusterID: remoteCluster, OriginIndex: 2},
		{OriginClusterID: remoteCluster, OriginIndex: 3},
	}); err != nil {
		t.Fatalf("seed applied: %v", err)
	}

	svc := &fakeService{leader: true}
	rec := NewReceiver(nil, svc, db, secret, "local")

	batch := Batch{Entries: []Envelope{
		{OriginIndex: 1, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert},
		{OriginIndex: 2, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert},
		{OriginIndex: 3, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert},
	}}
	w := postReplicate(t, rec, secret, remoteCluster, batch)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for all-deduped batch, got %d (%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt64(&svc.submitCnt); got != 0 {
		t.Errorf("SubmitCommand must not be called when all entries are duplicates, got %d calls", got)
	}
	var resp struct {
		Applied int `json:"applied"`
		Deduped int `json:"deduped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Applied != 0 || resp.Deduped != 3 {
		t.Errorf("expected applied=0 deduped=3, got %+v", resp)
	}
}

// A mixed batch (some duplicates, some fresh) must submit exactly once for the
// fresh entries, dedupe the rest, mark the fresh ones applied, and return 200.
func TestReceiverMixedBatchSubmitsFreshOnly(t *testing.T) {
	db := newTestDB(t)
	secret := "shared-secret"
	const remoteCluster = "remote"

	if err := MarkApplied(context.Background(), db, remoteCluster, "remote-node", 1); err != nil {
		t.Fatalf("seed applied: %v", err)
	}

	svc := &fakeService{leader: true}
	rec := NewReceiver(nil, svc, db, secret, "local")

	batch := Batch{Entries: []Envelope{
		{OriginIndex: 1, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert}, // dup
		{OriginIndex: 2, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert}, // fresh
		{OriginIndex: 3, OriginClusterID: remoteCluster, Type: raftcluster.CmdHostUpsert}, // fresh
	}}
	w := postReplicate(t, rec, secret, remoteCluster, batch)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt64(&svc.submitCnt); got != 1 {
		t.Errorf("expected exactly 1 SubmitCommand for the fresh entries, got %d", got)
	}
	// Fresh entries 2,3 must now be recorded as applied.
	set, err := AppliedIndexSet(context.Background(), db, remoteCluster, []uint64{2, 3})
	if err != nil {
		t.Fatalf("AppliedIndexSet: %v", err)
	}
	if len(set) != 2 {
		t.Errorf("expected fresh entries 2,3 marked applied, got %v", set)
	}
}
