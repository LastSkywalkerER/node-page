package appbackup

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ResticVersion is the release the installer fetches. Pinned rather than
// "latest" so every node in a cluster ends up on the same binary and a restore
// never meets a repository written by a newer format.
const ResticVersion = "0.19.1"

const resticReleaseBase = "https://github.com/restic/restic/releases/download/v" + ResticVersion

// BinaryPath is where the restic binary lives: inside the shared data volume,
// not the image. The user asked for restic to be installed on demand once the
// repository is configured, and the data volume is the one place that survives
// a container recreate.
func BinaryPath(dataDir string) string {
	return filepath.Join(dataDir, "bin", "restic")
}

// InstalledVersion reports the restic version already present, or "" when the
// binary is missing or unusable.
func InstalledVersion(dataDir string) string {
	bin := BinaryPath(dataDir)
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return ""
	}
	// "restic 0.19.1 compiled with go1.24 on linux/amd64"
	fields := strings.Fields(string(out))
	if len(fields) >= 2 && fields[0] == "restic" {
		return fields[1]
	}
	return ""
}

// EnsureBinary installs restic into the data volume when it is missing or on a
// different version. It verifies the download against the release's SHA256SUMS
// before writing anything executable — the same discipline the self-updater
// applies to node-stats' own binaries.
func EnsureBinary(ctx context.Context, dataDir string) (string, error) {
	bin := BinaryPath(dataDir)
	if InstalledVersion(dataDir) == ResticVersion {
		return bin, nil
	}

	asset := fmt.Sprintf("restic_%s_%s_%s.bz2", ResticVersion, runtime.GOOS, runtime.GOARCH)
	want, err := expectedSHA(ctx, asset)
	if err != nil {
		return "", err
	}

	blob, err := httpGet(ctx, resticReleaseBase+"/"+asset)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	sum := sha256.Sum256(blob)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}

	raw, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(blob)))
	if err != nil {
		return "", fmt.Errorf("decompress %s: %w", asset, err)
	}

	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", err
	}
	tmp := bin + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, bin); err != nil {
		return "", err
	}
	if v := InstalledVersion(dataDir); v != ResticVersion {
		return "", fmt.Errorf("installed restic reports version %q, expected %s", v, ResticVersion)
	}
	return bin, nil
}

// expectedSHA pulls the release SHA256SUMS and returns the digest for asset.
func expectedSHA(ctx context.Context, asset string) (string, error) {
	sums, err := httpGet(ctx, resticReleaseBase+"/SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in SHA256SUMS (unsupported platform %s/%s?)", asset, runtime.GOOS, runtime.GOARCH)
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// RepoURL renders the restic repository URL for a configuration.
func RepoURL(cfg RepoConfig) string {
	switch cfg.Backend {
	case BackendLocal:
		return cfg.Path
	case BackendSFTP:
		user := cfg.User
		if user != "" {
			user += "@"
		}
		return fmt.Sprintf("sftp:%s%s:%s", user, cfg.Host, cfg.RemotePath)
	case BackendS3:
		ep := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
		p := strings.Trim(cfg.Prefix, "/")
		if p != "" {
			p = "/" + p
		}
		return fmt.Sprintf("s3:%s/%s%s", strings.TrimSuffix(ep, "/"), cfg.Bucket, p)
	}
	return ""
}

// Resolve turns a stored connector into everything restic needs at run time.
func Resolve(cfg RepoConfig, sec RepoSecrets) ResolvedRepo {
	env := map[string]string{"RESTIC_PASSWORD": sec.Password}
	if cfg.Backend == BackendS3 {
		env["AWS_ACCESS_KEY_ID"] = cfg.AccessKeyID
		env["AWS_SECRET_ACCESS_KEY"] = sec.S3SecretKey
		if cfg.Region != "" {
			env["AWS_DEFAULT_REGION"] = cfg.Region
		}
	}
	return ResolvedRepo{URL: RepoURL(cfg), Env: env, SSHPrivateKey: sec.SSHPrivateKey}
}

// Runner executes restic against one repository.
type Runner struct {
	Bin  string
	Repo ResolvedRepo
	// Port is the SFTP port when non-default.
	Port int
}

// Run executes restic with the repository environment applied and returns the
// combined output. Secrets are passed via the environment, never argv, so they
// do not show up in the host's process list.
func (r Runner) Run(ctx context.Context, args ...string) (string, error) {
	extra, cleanup, err := r.sftpArgs()
	if err != nil {
		return "", err
	}
	defer cleanup()

	full := append([]string{"-r", r.Repo.URL}, extra...)
	full = append(full, args...)

	cmd := exec.CommandContext(ctx, r.Bin, full...)
	cmd.Env = append(os.Environ(), "RESTIC_PROGRESS_FPS=0")
	for k, v := range r.Repo.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// sftpArgs materialises the SSH private key (if any) and builds restic's
// sftp.args option. The key file is removed by the returned cleanup.
func (r Runner) sftpArgs() ([]string, func(), error) {
	noop := func() {}
	if !strings.HasPrefix(r.Repo.URL, "sftp:") {
		return nil, noop, nil
	}
	ssh := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "BatchMode=yes"}
	if r.Port != 0 && r.Port != 22 {
		ssh = append(ssh, "-p", fmt.Sprint(r.Port))
	}
	cleanup := noop
	if key := strings.TrimSpace(r.Repo.SSHPrivateKey); key != "" {
		f, err := os.CreateTemp("", "ns-restic-key-*")
		if err != nil {
			return nil, noop, err
		}
		if err := os.Chmod(f.Name(), 0o600); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, noop, err
		}
		if _, err := f.WriteString(key + "\n"); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, noop, err
		}
		f.Close()
		name := f.Name()
		cleanup = func() { os.Remove(name) }
		ssh = append(ssh, "-i", name)
	}
	// restic invokes: ssh <args> <host> -s sftp
	return []string{"-o", "sftp.args=" + strings.Join(ssh, " ")}, cleanup, nil
}

// EnsureRepo initialises the repository when it does not exist yet. An already
// initialised repository is left untouched.
func (r Runner) EnsureRepo(ctx context.Context) error {
	if _, err := r.Run(ctx, "cat", "config"); err == nil {
		return nil
	}
	out, err := r.Run(ctx, "init")
	if err != nil && !strings.Contains(out, "already initialized") {
		return fmt.Errorf("restic init: %s: %w", firstLines(out, 3), err)
	}
	return nil
}

// resticSnapshot is restic's own `snapshots --json` shape (subset). Snapshots
// written by restic >= 0.17 embed a summary, which is where the sizes come
// from — no extra pass over the repository is needed to show them.
type resticSnapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	Summary  *struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
		DataAdded           int64 `json:"data_added"`
	} `json:"summary,omitempty"`
}

// Snapshots lists the repository's snapshots, optionally filtered by tag.
func (r Runner) Snapshots(ctx context.Context, tags ...string) ([]Snapshot, error) {
	// --no-lock: listing must work against a repository the app container can
	// only read (a filesystem repo reached through a read-only HOST_ROOT mount).
	args := []string{"snapshots", "--json", "--no-lock"}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	out, err := r.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("restic snapshots: %s: %w", firstLines(out, 3), err)
	}
	var raw []resticSnapshot
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w (output: %s)", err, firstLines(out, 2))
	}
	snaps := make([]Snapshot, 0, len(raw))
	for _, s := range raw {
		snap := Snapshot{
			ID:       s.ID,
			ShortID:  s.ShortID,
			Time:     s.Time,
			Hostname: s.Hostname,
			Paths:    s.Paths,
			Tags:     s.Tags,
			Project:  tagValue(s.Tags, "project"),
			Kind:     tagValue(s.Tags, "kind"),
			Images:   decodeImagesTag(s.Tags),
			Moves:    decodeMovesTag(s.Tags),
		}
		if s.Summary != nil {
			snap.Size = s.Summary.TotalBytesProcessed
			snap.SizeAdded = s.Summary.DataAdded
		}
		snaps = append(snaps, snap)
	}
	return snaps, nil
}

// Stats reports what the repository costs on the far end. It walks the index,
// so it is a deliberate extra call rather than part of every status poll.
func (r Runner) Stats(ctx context.Context) (*RepoStats, error) {
	out, err := r.Run(ctx, "stats", "--json", "--mode", "raw-data", "--no-lock")
	if err != nil {
		return nil, fmt.Errorf("restic stats: %s: %w", firstLines(out, 3), err)
	}
	var s struct {
		TotalSize         int64   `json:"total_size"`
		TotalUncompressed int64   `json:"total_uncompressed_size"`
		CompressionRatio  float64 `json:"compression_ratio"`
		SnapshotsCount    int     `json:"snapshots_count"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return nil, fmt.Errorf("parse stats: %w (output: %s)", err, firstLines(out, 2))
	}
	return &RepoStats{
		TotalSize:        s.TotalSize,
		UncompressedSize: s.TotalUncompressed,
		CompressionRatio: s.CompressionRatio,
		SnapshotCount:    s.SnapshotsCount,
	}, nil
}

// Forget removes a snapshot and prunes the space it held.
func (r Runner) Forget(ctx context.Context, id string) error {
	// --retry-lock bounds the wait: restic's default is to block indefinitely,
	// which turns any lock problem into a hung UI action.
	out, err := r.Run(ctx, "forget", "--prune", "--retry-lock", "30s", id)
	if err != nil {
		return fmt.Errorf("restic forget %s: %s: %w", id, firstLines(out, 3), err)
	}
	return nil
}

// TagPrefix* are the structured restic tags node-stats writes on every
// snapshot. restic tags are opaque strings, so key=value is our convention.
const (
	tagProject = "project="
	tagKind    = "kind="
	tagHost    = "host="
	tagImages  = "images="
	tagMoves   = "moves="
)

// BuildTags renders the tag set for a snapshot.
//
// The image map is base64-encoded rather than stored as raw JSON: restic
// splits a --tag value on commas, so a JSON object with more than one service
// would arrive back as several broken tags. Base64's alphabet contains no
// comma, so the value survives the round trip whole.
func BuildTags(project, kind, host string, images map[string]string, moves []SnapshotMove) []string {
	tags := []string{tagProject + project, tagKind + kind}
	if host != "" {
		tags = append(tags, tagHost+host)
	}
	if len(images) > 0 {
		if b, err := json.Marshal(images); err == nil {
			tags = append(tags, tagImages+base64.RawURLEncoding.EncodeToString(b))
		}
	}
	if len(moves) > 0 {
		if b, err := json.Marshal(moves); err == nil {
			tags = append(tags, tagMoves+base64.RawURLEncoding.EncodeToString(b))
		}
	}
	return tags
}

// ProjectTag is the filter used to list one application's snapshots.
func ProjectTag(project string) string { return tagProject + project }

func tagValue(tags []string, key string) string {
	for _, t := range tags {
		switch key {
		case "project":
			if v, ok := strings.CutPrefix(t, tagProject); ok {
				return v
			}
		case "kind":
			if v, ok := strings.CutPrefix(t, tagKind); ok {
				return v
			}
		case "host":
			if v, ok := strings.CutPrefix(t, tagHost); ok {
				return v
			}
		}
	}
	return ""
}

func decodeMovesTag(tags []string) []SnapshotMove {
	for _, t := range tags {
		v, ok := strings.CutPrefix(t, tagMoves)
		if !ok {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			continue
		}
		var m []SnapshotMove
		if json.Unmarshal(raw, &m) == nil {
			return m
		}
	}
	return nil
}

func decodeImagesTag(tags []string) map[string]string {
	for _, t := range tags {
		v, ok := strings.CutPrefix(t, tagImages)
		if !ok {
			continue
		}
		var m map[string]string
		if raw, err := base64.RawURLEncoding.DecodeString(v); err == nil {
			if json.Unmarshal(raw, &m) == nil {
				return m
			}
		}
		// Snapshots written before the base64 encoding carried raw JSON; keep
		// reading them so an existing repository stays legible.
		if json.Unmarshal([]byte(v), &m) == nil {
			return m
		}
	}
	return nil
}

// firstLines trims command output for error messages so a failed restic run
// does not paste a screenful into a JSON status field.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}
