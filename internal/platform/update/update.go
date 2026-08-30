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
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
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

const checkInterval = time.Hour

// forcedCheckMinInterval rate-limits operator-triggered re-checks
// (GET /version?refresh=1) so the public endpoint can't hammer the GitHub API.
const forcedCheckMinInterval = time.Minute

// dateFallbackMargin guards the build-date update heuristic for non-semver
// builds: a release published within this window after the running build was
// produced is treated as "the same batch" (a tag typically follows its main
// push by minutes), not as an update.
const dateFallbackMargin = time.Hour

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
	// ManagedExternally is true when an orchestrator (dokploy, …) owns the
	// container lifecycle — in-app "update now" can't apply; redeploy there.
	ManagedExternally bool `json:"managed_externally,omitempty"`
	// DeployWebhookConfigured: a managed-externally deployment has the
	// orchestrator's deploy-webhook URL on file, so in-app updates work by
	// triggering it. The URL itself carries a deploy token and is only
	// readable through the admin settings endpoint, never here.
	DeployWebhookConfigured bool `json:"deploy_webhook_configured,omitempty"`
}

// PersistFn persists the auto-update toggle (to .env.agent). May be nil.
type PersistFn func(enabled bool) error

// DBStateFn reports the running DB engine + DSN, used to build a correct
// desired-state for a docker update when none exists yet (sqlite vs external pg).
type DBStateFn func() (dbType, dbDSN string)

// Service holds cached release state and the update plumbing.
type Service struct {
	repo           string
	dataDir        string
	persist        PersistFn
	dbState        DBStateFn
	persistWebhook func(url string) error
	client         *http.Client
	// dlClient downloads release assets (tens of MB): a 15s total budget and the
	// default 10s TLS-handshake timeout are too tight over slow links (SBCs /
	// LXCs), surfacing as "net/http: TLS handshake timeout". Patient + retried.
	dlClient *http.Client

	persistChannel func(channel string) error

	mu              sync.RWMutex
	latest          string
	latestPublished time.Time
	latestCommit    string // beta channel: latest commit SHA on main
	checkedAt       time.Time
	autoUpdate      bool
	channel         string // "stable" | "beta"
	webhookURL      string
	// lastAutoTarget is the Latest marker the auto-update loop last applied (a
	// release tag, or "main@<sha>" on the beta channel). An apply can fail to
	// clear "update available" — a webhook no-op (image tag lagging the release)
	// or a controller recreate that lands on the same image (e.g. a pinned
	// NODE_STATS_IMAGE) — so the loop must NOT re-fire for the same target on
	// every check (one runs 30s after every boot), which would reboot-loop the
	// stack. Persisted in dataDir because a successful apply replaces this container.
	lastAutoTarget string
}

// NewService constructs the update service. repo empty → DefaultRepo.
func NewService(repo, dataDir string, autoUpdate bool, persist PersistFn, dbState DBStateFn) *Service {
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}
	s := &Service{
		repo:    repo,
		dataDir: dataDir,
		persist: persist,
		dbState: dbState,
		client:  &http.Client{Timeout: 15 * time.Second},
		dlClient: &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   60 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
		autoUpdate: autoUpdate,
		channel:    channelStable,
	}
	s.loadWebhookState()
	return s
}

// Release channels the updater can follow.
const (
	channelStable = "stable"
	channelBeta   = "beta"
)

// normalizeChannel coerces arbitrary input to a known channel, defaulting to stable.
func normalizeChannel(c string) string {
	if strings.EqualFold(strings.TrimSpace(c), channelBeta) {
		return channelBeta
	}
	return channelStable
}

// WithReleaseChannel seeds the persisted release channel (from .env) and the
// persist function applied when an admin changes it at runtime.
func (s *Service) WithReleaseChannel(channel string, persist func(string) error) *Service {
	s.mu.Lock()
	s.channel = normalizeChannel(channel)
	s.mu.Unlock()
	s.persistChannel = persist
	return s
}

// ReleaseChannel reports the channel currently followed.
func (s *Service) ReleaseChannel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channel
}

// SetReleaseChannel validates, persists and applies a channel switch, then
// re-checks so /version reflects the new channel's latest immediately.
func (s *Service) SetReleaseChannel(ctx context.Context, channel string) error {
	ch := normalizeChannel(channel)
	if s.persistChannel != nil {
		if err := s.persistChannel(ch); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.channel = ch
	s.mu.Unlock()
	_ = s.Check(ctx)
	return nil
}

// WithDeployWebhook seeds the orchestrator deploy-webhook URL (from env /
// .env) and the persist function applied when an admin changes it at runtime.
func (s *Service) WithDeployWebhook(url string, persist func(string) error) *Service {
	s.mu.Lock()
	s.webhookURL = strings.TrimSpace(url)
	s.mu.Unlock()
	s.persistWebhook = persist
	return s
}

// DeployWebhookURL returns the configured orchestrator deploy-webhook URL
// (admin settings endpoint only — it embeds a deploy token).
func (s *Service) DeployWebhookURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webhookURL
}

// RedeployViaWebhook triggers the orchestrator deploy webhook to redeploy the
// stack (used to apply a settings change on a managed-externally deployment).
// Errors if no webhook is configured.
func (s *Service) RedeployViaWebhook() (string, error) {
	url := s.DeployWebhookURL()
	if url == "" {
		return "", fmt.Errorf("no deploy webhook configured — set it in the update settings, or redeploy in your orchestrator")
	}
	return s.triggerDeployWebhook(url)
}

// SetDeployWebhook validates, persists and applies the orchestrator
// deploy-webhook URL. An empty string clears it.
func (s *Service) SetDeployWebhook(rawURL string) error {
	u := strings.TrimSpace(rawURL)
	if u != "" {
		parsed, err := neturl.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("invalid webhook URL — expected the full http(s) deploy URL from the orchestrator")
		}
	}
	if s.persistWebhook != nil {
		if err := s.persistWebhook(u); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.webhookURL = u
	s.mu.Unlock()
	return nil
}

// Status returns the current build identity merged with cached update state.
func (s *Service) Status() Info {
	v := version.Get()
	s.mu.RLock()
	latest, checkedAt, auto := s.latest, s.checkedAt, s.autoUpdate
	channel, latestCommit := s.channel, s.latestCommit
	webhookSet := s.webhookURL != ""
	s.mu.RUnlock()
	info := Info{
		Current:                 v.Current,
		Commit:                  v.Commit,
		BuiltAt:                 v.BuiltAt,
		Deployment:              v.Deployment,
		Channel:                 channel,
		Latest:                  latest,
		AutoUpdate:              auto,
		ManagedExternally:       setup.ManagedExternally(),
		DeployWebhookConfigured: webhookSet,
	}
	if !checkedAt.IsZero() {
		info.CheckedAt = checkedAt.UTC().Format(time.RFC3339)
	}
	if channel == channelBeta {
		// Docker beta is the rolling :beta image built from main HEAD — there is no
		// release to semver-compare against. "Newer beta" = main has a commit other
		// than the one this build was cut from. Show the short SHA as the latest
		// marker so the popover isn't comparing against a stale release tag.
		if v.Deployment == "docker" {
			if latestCommit != "" {
				info.Latest = "main@" + shortSHA(latestCommit)
				info.UpdateAvailable = v.Commit != "" && !sameCommit(v.Commit, latestCommit)
			}
			return info
		}
		// Native beta self-updates from release assets, which only exist for tagged
		// releases — so it follows the newest prerelease RELEASE tag (already cached
		// in `latest`), not main HEAD.
		info.UpdateAvailable = betaUpdateAvailable(v.Current, latest)
		return info
	}
	info.UpdateAvailable = stableUpdateAvailable(v.Current, latest, v.Deployment)
	return info
}

// betaUpdateAvailable decides whether the beta channel should offer `latest`
// (the newest prerelease release tag) to a native build. Like the stable check
// it never nags a non-semver dev build (go run / air), and it only moves forward
// via semver + prerelease precedence — so beta.19 → beta.20 upgrades, but a
// re-listed older prerelease does not. Pure (no globals) for testing.
func betaUpdateAvailable(current, latest string) bool {
	if latest == "" || sameRelease(current, latest) {
		return false
	}
	if _, ok := parseSemver(current); !ok {
		return false
	}
	return newerAvailable(current, latest)
}

// stableUpdateAvailable decides whether the stable channel should offer `latest`
// to a build identified by (current, deployment). The latest stable release is
// offered whenever the running build is not THAT exact release — regardless of
// version ordering. So a beta build can move to stable (even to a "lower" stable
// than the running beta), and a channel switch always surfaces that channel's
// latest; a build already on the newest tag shows nothing. A native non-semver
// dev build (go run/air) is never nagged. Pure (no globals) for testing.
func stableUpdateAvailable(current, latest, deployment string) bool {
	if latest == "" || sameRelease(current, latest) {
		return false
	}
	if _, isSemver := parseSemver(current); isSemver {
		return true
	}
	// Non-semver: docker images (beta/main) can move to stable; a native dev
	// build is local work — don't nag it.
	return deployment == "docker"
}

// sameRelease reports whether two version strings name the same release,
// tolerant of a leading "v" and surrounding space/case ("0.7.6" == "v0.7.6").
func sameRelease(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "v")
	}
	return norm(a) == norm(b)
}

// shortSHA returns the first 7 chars of a git SHA (or the whole string if shorter).
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// sameCommit compares two SHAs tolerant of short/long forms.
func sameCommit(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// RefreshIfStale forces a release re-check when the cached state is older than
// maxAge, rate-limited to forcedCheckMinInterval so the public /version
// endpoint can't be abused into hammering the GitHub API. Best-effort.
func (s *Service) RefreshIfStale(ctx context.Context, maxAge time.Duration) {
	s.mu.RLock()
	checkedAt := s.checkedAt
	s.mu.RUnlock()
	age := time.Since(checkedAt)
	if !checkedAt.IsZero() && (age < maxAge || age < forcedCheckMinInterval) {
		return
	}
	if err := s.Check(ctx); err != nil {
		log.Debug("update: forced check failed", "error", err)
	}
}

// buildTime resolves when the running build was produced: the ldflags-injected
// date when parseable, otherwise the executable's own mtime — which IS the
// build time for container images built from source without build args (e.g.
// dokploy building the repo's Dockerfile, where VERSION/DATE stay defaults).
func buildTime(builtAt string) time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(builtAt)); err == nil {
		return t
	}
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			return st.ModTime()
		}
	}
	return time.Time{}
}

// dateBasedUpdateAvailable is the update heuristic for builds whose version is
// NOT a semver (dev / "docker" / source-built images): a release published
// meaningfully after this build was produced is an update. The semver compare
// stays authoritative for tagged builds; this only widens detection where it
// was previously impossible.
func dateBasedUpdateAvailable(deployment, current, latest string, built, published time.Time) bool {
	if _, currentIsSemver := parseSemver(current); currentIsSemver {
		return false // tagged build → semver compare already decided
	}
	if _, ok := parseSemver(latest); !ok {
		return false
	}
	if deployment != "docker" {
		// A native dev build (go run / air) is a developer workstation — never
		// nag it to "update" to a release.
		return false
	}
	if built.IsZero() || published.IsZero() {
		return false
	}
	return published.After(built.Add(dateFallbackMargin))
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
		s.autoApply(ctx)
	}
	return nil
}

// autoApply runs one unattended update attempt (auto-update toggle on).
// Managed-externally deployments update through the orchestrator's deploy
// webhook; since that redeploy can be a no-op (image tag lagging the GitHub
// release), the same release tag is never re-triggered — only a NEW latest
// fires the webhook again. Manual "update now" has no such guard.
func (s *Service) autoApply(ctx context.Context) {
	st := s.Status()
	s.mu.RLock()
	webhook, last := s.webhookURL, s.lastAutoTarget
	s.mu.RUnlock()
	if !shouldAutoApply(st, webhook != "", last) {
		return
	}
	if _, err := s.UpdateNow(ctx); err != nil {
		log.Error("update: auto-update apply failed", "error", err)
		return
	}
	// Record the applied target so the same one isn't auto-applied again next
	// check/boot (the reboot-loop guard).
	s.rememberAutoTarget(st.Latest)
}

// shouldAutoApply decides whether the auto-update loop should apply now:
//   - an update must be available;
//   - a managed-externally deployment needs a deploy webhook to apply at all;
//   - the target must not already have been auto-applied. An apply that doesn't
//     clear "update available" (a webhook no-op when the image tag lags the
//     release, or a controller recreate landing on the same image because a
//     pinned NODE_STATS_IMAGE shadows ds.Image) would otherwise re-fire on every
//     hourly check AND every boot — the loop runs a check 30s after start — and
//     reboot-loop the stack. A genuinely newer release has a different target and
//     still applies. Manual "Update now" doesn't go through here, so it's never
//     suppressed.
func shouldAutoApply(st Info, webhookConfigured bool, lastTarget string) bool {
	if !st.UpdateAvailable {
		return false
	}
	if st.ManagedExternally && !webhookConfigured {
		return false
	}
	if st.Latest == "" || st.Latest == lastTarget {
		return false
	}
	return true
}

// Check polls the latest GitHub release tag and caches it. On the beta channel
// it also caches main's HEAD commit, the rolling :beta image's "latest".
func (s *Service) Check(ctx context.Context) error {
	rel, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return err
	}
	latest := ""
	var published time.Time
	if rel != nil {
		latest = rel.TagName
		if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
			published = t
		}
	}
	latestCommit := ""
	// The rolling :beta image (compared by commit) is a docker-only concept: a
	// native beta build self-updates from the latest prerelease RELEASE tag, so it
	// needs no commit lookup. Skipping it on native also avoids a needless GitHub
	// API call and its failure path.
	if s.ReleaseChannel() == channelBeta && version.Deployment() == "docker" {
		// Best-effort: a commit-fetch failure shouldn't fail the whole check.
		if sha, cerr := s.fetchLatestMainCommit(ctx); cerr == nil {
			latestCommit = sha
		} else {
			log.Debug("update: beta main-commit check failed", "error", cerr)
		}
	}
	s.mu.Lock()
	s.latest = latest
	s.latestPublished = published
	s.latestCommit = latestCommit
	s.checkedAt = time.Now()
	s.mu.Unlock()
	return nil
}

// fetchLatestMainCommit returns the SHA of main's current HEAD.
func (s *Service) fetchLatestMainCommit(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/main", s.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github commits API returned %s", resp.Status)
	}
	var c struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return "", err
	}
	return c.SHA, nil
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
			} else if s.AutoUpdate() {
				s.autoApply(ctx)
			}
			t.Reset(checkInterval)
		}
	}()
}

// updateDocker bumps the desired-state so the controller pulls the new image and
// recreates the app, preserving the current DB topology.
func (s *Service) updateDocker() (string, error) {
	// When an external orchestrator (dokploy, portainer, …) owns the lifecycle
	// there is no controller to apply the desired-state. With its deploy
	// webhook on file we can still update — the orchestrator pulls the latest
	// image and redeploys; otherwise point the operator at the right tool
	// instead of silently writing a file nothing reads.
	if setup.ManagedExternally() {
		if url := s.DeployWebhookURL(); url != "" {
			return s.triggerDeployWebhook(url)
		}
		return "", fmt.Errorf("this deployment is managed by an external orchestrator — redeploy there, or paste its deploy-webhook URL (dokploy: Deployments tab) into the update settings so node-stats can trigger it")
	}
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
	// Pin the image tag to the followed channel so a beta-channel update pulls
	// the moving :beta image and a stable one pulls :latest.
	ds.Image = channelImage(s.ReleaseChannel())
	ds.PullBeforeApply = true
	ds.Generation++
	if err := setup.WriteDesiredState(s.dataDir, *ds); err != nil {
		return "", fmt.Errorf("failed to request update from the controller: %w", err)
	}
	return "Pulling the latest image and recreating the stack — this can take a moment.", nil
}

// channelImage returns the container image reference for a release channel:
// the published repo (from setup.DefaultImage) with the :latest tag for stable
// or :beta for beta. Mirrors the tags the CI workflow publishes.
func channelImage(channel string) string {
	base := setup.DefaultImage
	if i := strings.LastIndex(base, ":"); i > strings.LastIndex(base, "/") {
		base = base[:i] // strip the existing tag
	}
	if channel == channelBeta {
		return base + ":beta"
	}
	return base + ":latest"
}

// webhookStateFile remembers (in dataDir, which survives the redeploy/recreate)
// the Latest marker the auto-update loop last applied. See lastAutoTarget.
const webhookStateFile = "webhook-update.json"

// triggerDeployWebhook asks the orchestrator to redeploy the stack; with
// pull_policy: always (the dokploy compose default here) that pulls the
// newest image. POST first — that's what git providers send to these URLs —
// and fall back to GET when the route only accepts that. The URL embeds a
// deploy token, so it must never reach logs or error strings unredacted.
func (s *Service) triggerDeployWebhook(url string) (string, error) {
	do := func(method string) (*http.Response, error) {
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			return nil, err
		}
		return s.client.Do(req)
	}
	resp, err := do(http.MethodPost)
	if err == nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
		resp.Body.Close()
		resp, err = do(http.MethodGet)
	}
	if err != nil {
		return "", fmt.Errorf("deploy webhook call failed: %w", redactURLInError(err, url))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("deploy webhook returned %s — re-copy the URL from the orchestrator's Deployments tab", resp.Status)
	}
	log.Info("update: orchestrator deploy webhook triggered", "host", redactURL(url))
	return "Triggered the orchestrator's deploy webhook — it will pull the latest image and redeploy shortly.", nil
}

// rememberAutoTarget persists the Latest marker the auto-update loop just
// applied, so the same target isn't auto-applied again on the next check/boot
// (see lastAutoTarget). Only the auto-update loop calls this — manual "Update
// now" and manual settings redeploys deliberately don't, so they're never
// suppressed.
func (s *Service) rememberAutoTarget(tag string) {
	s.mu.Lock()
	s.lastAutoTarget = tag
	s.mu.Unlock()
	if s.dataDir == "" || tag == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{
		"last_triggered_tag": tag,
		"at":                 time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.WriteFile(filepath.Join(s.dataDir, webhookStateFile), b, 0o644); err != nil {
		log.Debug("update: persist auto-update state failed", "error", err)
	}
}

func (s *Service) loadWebhookState() {
	if s.dataDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(s.dataDir, webhookStateFile))
	if err != nil {
		return
	}
	var st struct {
		LastTriggeredTag string `json:"last_triggered_tag"`
	}
	if json.Unmarshal(b, &st) == nil {
		s.lastAutoTarget = st.LastTriggeredTag
	}
}

// redactURLInError strips the token-bearing webhook URL out of transport
// errors (url.Error embeds the full URL in its message).
func redactURLInError(err error, rawURL string) error {
	return errors.New(strings.ReplaceAll(err.Error(), rawURL, redactURL(rawURL)))
}

func redactURL(raw string) string {
	if u, err := neturl.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/…"
	}
	return "<webhook-url>"
}

// ---- GitHub release model ----

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt string    `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []ghAsset `json:"assets"`
}

// fetchLatestRelease returns the newest release for the followed channel.
//
//   - stable: GitHub's /releases/latest already excludes drafts and
//     prereleases, so a single cheap request suffices.
//   - beta: the newest non-draft release INCLUDING prereleases — /releases/latest
//     would skip a `-beta` prerelease, so we list /releases (sorted newest-first)
//     and take the first non-draft.
func (s *Service) fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	if s.ReleaseChannel() == channelBeta {
		return s.fetchLatestFromList(ctx)
	}
	return s.fetchGitHub(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", s.repo), false)
}

// fetchLatestFromList pulls the recent releases page and returns the newest
// non-draft entry (prerelease or full) — the beta channel's "latest".
func (s *Service) fetchLatestFromList(ctx context.Context) (*ghRelease, error) {
	rel, err := s.fetchGitHub(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", s.repo), true)
	if err != nil {
		return nil, err
	}
	return rel, nil
}

// fetchGitHub GETs a releases endpoint. When list is true the body is a JSON
// array and the first non-draft release is returned; otherwise it's a single
// release object.
func (s *Service) fetchGitHub(ctx context.Context, url string, list bool) (*ghRelease, error) {
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
	if !list {
		var rel ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return nil, err
		}
		return &rel, nil
	}
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	return newestRelease(rels), nil
}

// newestRelease picks the highest-versioned non-draft release. GitHub's
// /releases list is NOT ordered by version (it follows tag creation order and
// happily lists beta.9 above beta.10/beta.11), so "first entry" silently pinned
// the beta channel to an older prerelease. Non-semver tags never win over a
// semver one; among non-semver-only lists the first non-draft is returned.
func newestRelease(rels []ghRelease) *ghRelease {
	var best *ghRelease
	for i := range rels {
		r := &rels[i]
		if r.Draft {
			continue
		}
		if best == nil {
			best = r
			continue
		}
		_, bestSemver := parseSemver(best.TagName)
		_, rSemver := parseSemver(r.TagName)
		switch {
		case rSemver && !bestSemver:
			best = r
		case rSemver && bestSemver && newerAvailable(best.TagName, r.TagName):
			best = r
		}
	}
	return best
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
	// Equal numeric base: fall back to semver pre-release precedence.
	cp, lp := isPrerelease(current), isPrerelease(latest)
	if cp != lp {
		// A full release outranks any prerelease of the same base (0.7.6-beta.19 <
		// 0.7.6), so the stable release IS newer than a beta build of the same
		// version — and a prerelease is never "newer" than the matching release.
		return cp && !lp
	}
	if cp && lp {
		// Two prereleases of the same base (beta.19 vs beta.20): compare suffixes so
		// the beta channel can roll forward between prereleases.
		return comparePrerelease(current, latest) < 0
	}
	return false // identical release
}

// comparePrerelease orders two semver pre-release suffixes (the part after '-')
// per semver §11: dot-separated identifiers compared left-to-right, numeric ones
// numerically (so beta.10 > beta.9), numeric identifiers ranking below
// alphanumeric, and — all else equal — the longer identifier list winning.
// Returns <0 when a<b, 0 when equal, >0 when a>b.
func comparePrerelease(a, b string) int {
	ai, bi := prereleaseIdentifiers(a), prereleaseIdentifiers(b)
	for i := 0; i < len(ai) && i < len(bi); i++ {
		an, aNum := atoiOK(ai[i])
		bn, bNum := atoiOK(bi[i])
		switch {
		case aNum && bNum:
			if an != bn {
				return sign(an - bn)
			}
		case aNum != bNum:
			if aNum { // numeric < alphanumeric
				return -1
			}
			return 1
		default:
			if ai[i] != bi[i] {
				if ai[i] < bi[i] {
					return -1
				}
				return 1
			}
		}
	}
	return sign(len(ai) - len(bi))
}

// prereleaseIdentifiers extracts the dot-separated prerelease identifiers from a
// version's "-suffix" (build metadata after '+' is dropped). Empty when there is
// no prerelease part.
func prereleaseIdentifiers(v string) []string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	dash := strings.IndexByte(v, '-')
	if dash < 0 {
		return nil
	}
	suffix := v[dash+1:]
	if plus := strings.IndexByte(suffix, '+'); plus >= 0 {
		suffix = suffix[:plus]
	}
	if suffix == "" {
		return nil
	}
	return strings.Split(suffix, ".")
}

func atoiOK(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// isPrerelease reports whether a version carries a pre-release suffix over a
// valid dotted-numeric base (e.g. "0.7.6-beta.19"). Plain releases ("0.7.5")
// and non-semver builds ("main") are not prereleases.
func isPrerelease(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(v, "-+"); idx <= 0 {
		return false
	}
	_, ok := parseSemver(v)
	return ok
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
