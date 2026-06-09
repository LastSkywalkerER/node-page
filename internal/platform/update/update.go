// Package update implements GitHub-Releases-based update checking and applying.
//
// It exposes the current build identity plus the latest available release, a
// persisted "auto-update" toggle, and an "update now" action. Docker
// deployments update by asking the controller sidecar to pull the new image and
// recreate the app (via the desired-state descriptor); native binaries
// self-replace from the release asset (`node-stats update`).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/setup"
	"system-stats/internal/version"
)

// DefaultRepo is the GitHub repo polled for releases (owner/name).
const DefaultRepo = "LastSkywalkerER/node-page"

const checkInterval = 6 * time.Hour

// Info is the JSON returned by GET /api/v1/version — build identity + update state.
type Info struct {
	Current         string `json:"current"`
	Commit          string `json:"commit,omitempty"`
	BuiltAt         string `json:"built_at,omitempty"`
	Deployment      string `json:"deployment"` // docker | native
	Channel         string `json:"channel"`    // stable
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	AutoUpdate      bool   `json:"auto_update"`
	CheckedAt       string `json:"checked_at,omitempty"`
}

// PersistFn persists the auto-update toggle (to .env.agent). May be nil.
type PersistFn func(enabled bool) error

// DBStateFn reports the running DB engine + DSN, used to build a correct
// desired-state for a docker update when none exists yet (sqlite vs external pg).
type DBStateFn func() (dbType, dbDSN string)

// Service holds cached release state and the update plumbing.
type Service struct {
	repo    string
	dataDir string
	persist PersistFn
	dbState DBStateFn
	client  *http.Client

	mu         sync.RWMutex
	latest     string
	checkedAt  time.Time
	autoUpdate bool
}

// NewService constructs the update service. repo empty → DefaultRepo.
func NewService(repo, dataDir string, autoUpdate bool, persist PersistFn, dbState DBStateFn) *Service {
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}
	return &Service{
		repo:       repo,
		dataDir:    dataDir,
		persist:    persist,
		dbState:    dbState,
		client:     &http.Client{Timeout: 15 * time.Second},
		autoUpdate: autoUpdate,
	}
}

// Status returns the current build identity merged with cached update state.
func (s *Service) Status() Info {
	v := version.Get()
	s.mu.RLock()
	latest, checkedAt, auto := s.latest, s.checkedAt, s.autoUpdate
	s.mu.RUnlock()
	info := Info{
		Current:    v.Current,
		Commit:     v.Commit,
		BuiltAt:    v.BuiltAt,
		Deployment: v.Deployment,
		Channel:    "stable",
		Latest:     latest,
		AutoUpdate: auto,
	}
	if !checkedAt.IsZero() {
		info.CheckedAt = checkedAt.UTC().Format(time.RFC3339)
	}
	info.UpdateAvailable = newerAvailable(v.Current, latest)
	return info
}

// AutoUpdate reports the current toggle state.
func (s *Service) AutoUpdate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoUpdate
}

// SetAutoUpdate persists and applies the toggle. When enabling and an update is
// already available, it triggers one immediately.
func (s *Service) SetAutoUpdate(ctx context.Context, enabled bool) error {
	s.mu.Lock()
	s.autoUpdate = enabled
	s.mu.Unlock()
	if s.persist != nil {
		if err := s.persist(enabled); err != nil {
			return err
		}
	}
	if enabled {
		_ = s.Check(ctx)
		if s.Status().UpdateAvailable {
			if _, err := s.UpdateNow(ctx); err != nil {
				log.Error("update: auto-update apply failed", "error", err)
			}
		}
	}
	return nil
}

// Check polls the latest GitHub release tag and caches it.
func (s *Service) Check(ctx context.Context) error {
	rel, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return err
	}
	latest := ""
	if rel != nil {
		latest = rel.TagName
	}
	s.mu.Lock()
	s.latest = latest
	s.checkedAt = time.Now()
	s.mu.Unlock()
	return nil
}

// UpdateNow applies the latest release. Docker → tell the controller to pull +
// recreate; native → self-replace the binary. Returns a user-facing message.
func (s *Service) UpdateNow(ctx context.Context) (string, error) {
	if version.Deployment() == "docker" {
		return s.updateDocker()
	}
	return s.updateNative(ctx)
}

// Start runs the periodic check loop; auto-applies when the toggle is on.
func (s *Service) Start(ctx context.Context) {
	go func() {
		// Initial check shortly after boot, then on the interval.
		t := time.NewTimer(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if err := s.Check(ctx); err != nil {
				log.Debug("update: check failed", "error", err)
			} else if s.AutoUpdate() && s.Status().UpdateAvailable {
				if _, err := s.UpdateNow(ctx); err != nil {
					log.Error("update: auto-update failed", "error", err)
				}
			}
			t.Reset(checkInterval)
		}
	}()
}

// updateDocker bumps the desired-state so the controller pulls the new image and
// recreates the app, preserving the current DB topology.
func (s *Service) updateDocker() (string, error) {
	ds, _ := setup.ReadDesiredState(s.dataDir)
	if ds == nil {
		// No prior switch recorded (e.g. a plain sqlite install): construct a
		// descriptor matching the running DB so the regenerated compose is correct.
		mode := setup.DBModeSQLite
		dsn := ""
		if s.dbState != nil {
			if t, d := s.dbState(); strings.EqualFold(t, "postgres") {
				mode, dsn = setup.DBModePostgresExternal, d
			}
		}
		ds = &setup.DesiredState{DBMode: mode, DBDSN: dsn}
	}
	ds.PullBeforeApply = true
	ds.Generation++
	if err := setup.WriteDesiredState(s.dataDir, *ds); err != nil {
		return "", fmt.Errorf("failed to request update from the controller: %w", err)
	}
	return "Pulling the latest image and recreating the stack — this can take a moment.", nil
}

// ---- GitHub release model ----

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func (s *Service) fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", s.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Repo has no published releases yet — not an error, just "nothing newer".
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases API returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// ---- semver compare ----

// newerAvailable reports whether latest is a strictly newer semver than current.
// Returns false unless BOTH parse as vX.Y.Z (so dev/main/sha builds never claim
// an update).
func newerAvailable(current, latest string) bool {
	c, okC := parseSemver(current)
	l, okL := parseSemver(latest)
	if !okC || !okL {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseSemver parses "v1.2.3" / "1.2.3" (ignoring any pre-release/build suffix)
// into [3]int. ok=false when it isn't a dotted numeric version.
func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return out, false
	}
	// Drop pre-release/build metadata (-rc1, +build).
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// assetBaseName is the release asset base name for this build's OS/arch, e.g.
// "node-stats_v1.2.3_linux_amd64". Matches the release.yml naming.
func assetBaseName(tag string) string {
	return fmt.Sprintf("node-stats_%s_%s_%s", tag, runtime.GOOS, runtime.GOARCH)
}
