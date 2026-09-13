package di

import (
	"context"
	"testing"
	"time"

	raftcluster "system-stats/internal/cluster/raft"
)

// fakeRaftService answers Status/Enabled; every other method is unused by the
// catch-up gate and would panic on the nil embedded interface if it ever were.
type fakeRaftService struct {
	raftcluster.Service
	status raftcluster.Status
}

func (f *fakeRaftService) Status() raftcluster.Status { return f.status }
func (f *fakeRaftService) Enabled() bool              { return f.status.Enabled }

func containerWith(status raftcluster.Status) (*Container, *fakeRaftService) {
	svc := &fakeRaftService{status: status}
	return &Container{raftSwap: raftcluster.NewSwappableService(svc)}, svc
}

// The backfills republish rows read from the LOCAL tables, and raft rewinds
// those tables to the newest snapshot on every start. Until the replay has
// refilled them a backfill would publish snapshot-age rows as the current ones —
// which is how gateway routes edited minutes earlier came back old after a
// restart.
func TestRaftCaughtUp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status raftcluster.Status
		want   bool
	}{
		{"still applying the log tail", raftcluster.Status{Enabled: true, AppliedIndex: 2428500, CommitIndex: 2432427}, false},
		{"caught up", raftcluster.Status{Enabled: true, AppliedIndex: 2432427, CommitIndex: 2432427}, true},
		{"ahead of the last commit it knows", raftcluster.Status{Enabled: true, AppliedIndex: 2432430, CommitIndex: 2432427}, true},
		{"nothing committed yet", raftcluster.Status{Enabled: true, AppliedIndex: 0, CommitIndex: 0}, false},
		{"raft off", raftcluster.Status{Enabled: false, AppliedIndex: 10, CommitIndex: 10}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := containerWith(tc.status)
			if got := c.raftCaughtUp(); got != tc.want {
				t.Fatalf("raftCaughtUp() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A backfill that runs once per activation waits for the replay rather than
// publishing blind, and gives up rather than publishing late.
func TestWaitForLogCatchUp(t *testing.T) {
	t.Parallel()

	t.Run("returns once the replay finishes", func(t *testing.T) {
		c, svc := containerWith(raftcluster.Status{Enabled: true, AppliedIndex: 1, CommitIndex: 100})
		go func() {
			time.Sleep(50 * time.Millisecond)
			svc.status = raftcluster.Status{Enabled: true, AppliedIndex: 100, CommitIndex: 100}
		}()
		if !c.waitForLogCatchUp(context.Background(), 5*time.Second) {
			t.Fatal("wait gave up although the node caught up")
		}
	})

	t.Run("gives up when the node stays behind", func(t *testing.T) {
		c, _ := containerWith(raftcluster.Status{Enabled: true, AppliedIndex: 1, CommitIndex: 100})
		if c.waitForLogCatchUp(context.Background(), 50*time.Millisecond) {
			t.Fatal("wait reported a catch-up that never happened")
		}
	})

	t.Run("returns on a cancelled context", func(t *testing.T) {
		c, _ := containerWith(raftcluster.Status{Enabled: true, AppliedIndex: 1, CommitIndex: 100})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if c.waitForLogCatchUp(ctx, time.Minute) {
			t.Fatal("wait ignored the cancelled context")
		}
	})
}
