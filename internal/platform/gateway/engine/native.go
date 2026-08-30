package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/setup"
)

// Native (systemd) gateway backend — for .deb / Proxmox-LXC / any non-Docker
// Linux install where there is no controller sidecar. node-stats itself
// downloads the Traefik binary, writes a static config + a systemd unit and
// drives it with systemctl. Requires root (the packaged unit runs as root) and
// systemd (/run/systemd/system).
//
// Layout (dataDir = the process working dir on native installs, e.g.
// /var/lib/node-stats):
//
//	<dataDir>/traefik/bin/traefik        the binary (+ VERSION marker)
//	<dataDir>/traefik/traefik.yml        static config (entrypoints, file provider, ACME)
//	<dataDir>/traefik/dynamic/node-stats.yml   rendered routes (same as managed docker)
//	<dataDir>/traefik/acme/acme.json     Let's Encrypt state
//	/etc/systemd/system/node-stats-traefik.service

const (
	nativeUnitName = "node-stats-traefik"
	nativeUnitPath = "/etc/systemd/system/" + nativeUnitName + ".service"
	// nativeTraefikFallbackVersion is used when the GitHub API can't be reached
	// to resolve the newest v3 release. Override with NODE_STATS_TRAEFIK_VERSION.
	nativeTraefikFallbackVersion = "v3.3.7"
	nativePingURL                = "http://127.0.0.1:8082/ping"
)

// nativeProvisioner manages the systemd-run Traefik.
type nativeProvisioner struct {
	logger  *log.Logger
	dataDir string
	// installedWant caches the last successfully applied provision so an
	// unchanged cycle doesn't touch systemd.
	installedWant *setup.GatewayProvision
	lastCheck     time.Time
}

func newNativeProvisioner(logger *log.Logger, dataDir string) *nativeProvisioner {
	return &nativeProvisioner{logger: logger, dataDir: dataDir}
}

// nativeAvailable reports whether this process can run the systemd backend,
// with a human-readable reason when it can't.
func nativeAvailable() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "native managed mode needs Linux with systemd"
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false, "systemd is not running on this machine"
	}
	if os.Geteuid() != 0 {
		return false, "node-stats must run as root to manage a Traefik systemd service"
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, "systemctl not found"
	}
	return true, ""
}

func (n *nativeProvisioner) dir() string        { return filepath.Join(n.dataDir, "traefik") }
func (n *nativeProvisioner) binPath() string    { return filepath.Join(n.dir(), "bin", "traefik") }
func (n *nativeProvisioner) staticPath() string { return filepath.Join(n.dir(), "traefik.yml") }
func (n *nativeProvisioner) dynamicDir() string { return filepath.Join(n.dir(), "dynamic") }
func (n *nativeProvisioner) acmeDir() string    { return filepath.Join(n.dir(), "acme") }

// Reconcile makes the systemd Traefik match want (installing on first use).
// Returns whether the service was (re)started.
func (n *nativeProvisioner) Reconcile(ctx context.Context, want setup.GatewayProvision) (restarted bool, err error) {
	if n.installedWant != nil && *n.installedWant == want && time.Since(n.lastCheck) < time.Minute {
		return false, nil
	}
	if err := os.MkdirAll(n.dynamicDir(), 0o755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(n.acmeDir(), 0o700); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Join(n.dir(), "logs"), 0o755); err != nil {
		return false, err
	}
	if err := n.ensureBinary(ctx); err != nil {
		return false, fmt.Errorf("install traefik: %w", err)
	}
	staticChanged, err := writeIfChangedReport(n.staticPath(), n.renderStatic(want))
	if err != nil {
		return false, err
	}
	unitChanged, err := writeIfChangedReport(nativeUnitPath, n.renderUnit())
	if err != nil {
		return false, err
	}
	if unitChanged {
		if out, err := systemctl(ctx, "daemon-reload"); err != nil {
			return false, fmt.Errorf("daemon-reload: %s: %w", out, err)
		}
	}
	active := n.isActive(ctx)
	switch {
	case !active:
		if out, err := systemctl(ctx, "enable", "--now", nativeUnitName); err != nil {
			return false, fmt.Errorf("enable --now: %s: %w", out, err)
		}
		restarted = true
	case staticChanged || unitChanged:
		if out, err := systemctl(ctx, "restart", nativeUnitName); err != nil {
			return false, fmt.Errorf("restart: %s: %w", out, err)
		}
		restarted = true
	}
	w := want
	n.installedWant = &w
	n.lastCheck = time.Now()
	return restarted, nil
}

// Disable stops and removes the unit (binary and ACME state are kept so a
// re-enable is instant and keeps its certificates). No-op when never installed.
func (n *nativeProvisioner) Disable(ctx context.Context) error {
	n.installedWant = nil
	if _, err := os.Stat(nativeUnitPath); err != nil {
		return nil
	}
	if out, err := systemctl(ctx, "disable", "--now", nativeUnitName); err != nil {
		n.logger.Warn("gateway: disable traefik unit", "out", out, "error", err)
	}
	if err := os.Remove(nativeUnitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = systemctl(ctx, "daemon-reload")
	n.logger.Info("gateway: removed native traefik service")
	return nil
}

// Logs returns the unit's recent journal lines (traefik log + access log).
func (n *nativeProvisioner) Logs(ctx context.Context, tail int) (string, error) {
	rc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(rc, "journalctl", "-u", nativeUnitName, "-n", fmt.Sprint(tail), "--no-pager", "-o", "short-iso").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("journalctl: %w", err)
	}
	return string(out), nil
}

// Health reports the unit state and, when failed, the last journal lines.
func (n *nativeProvisioner) Health(ctx context.Context) (healthy bool, detail string) {
	if pingURL(ctx, nativePingURL) {
		return true, "active"
	}
	state, _ := systemctl(ctx, "is-active", nativeUnitName)
	state = strings.TrimSpace(state)
	if state == "failed" || state == "activating" {
		rc, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(rc, "journalctl", "-u", nativeUnitName, "-n", "3", "--no-pager", "-o", "cat").CombinedOutput()
		if s := strings.TrimSpace(string(out)); s != "" {
			return false, state + ": " + s
		}
	}
	if state == "" {
		state = "not installed"
	}
	if owners := portOwners(ctx, n.lastPorts()); owners != "" {
		return false, state + " — " + owners
	}
	return false, state
}

// lastPorts returns the entrypoint ports of the last applied provision.
func (n *nativeProvisioner) lastPorts() []int {
	if n.installedWant == nil {
		return []int{80, 443}
	}
	hp, sp := n.installedWant.HTTPPort, n.installedWant.HTTPSPort
	if hp <= 0 {
		hp = 80
	}
	if sp <= 0 {
		sp = 443
	}
	return []int{hp, sp}
}

// portOwners reports which OTHER processes listen on the given ports (via
// `ss -ltnp`), e.g. "port 80 is held by nginx (pid 1234)" — the usual reason a
// managed Traefik fails to start: another reverse proxy already owns :80/:443.
func portOwners(ctx context.Context, ports []int) string {
	rc, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(rc, "ss", "-ltnpH").Output()
	if err != nil {
		return ""
	}
	var msgs []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		i := strings.LastIndexByte(local, ':')
		if i < 0 {
			continue
		}
		p, _ := strconv.Atoi(local[i+1:])
		for _, want := range ports {
			if p != want {
				continue
			}
			proc := ""
			if j := strings.Index(line, "users:(("); j >= 0 {
				rest := line[j+8:]
				if k := strings.IndexByte(rest, ')'); k > 0 {
					proc = strings.ReplaceAll(rest[:k], "\"", "")
				}
			}
			if strings.Contains(proc, "traefik") {
				continue
			}
			if proc == "" {
				proc = "another process"
			}
			msgs = append(msgs, fmt.Sprintf("port %d is already held by %s", p, proc))
		}
	}
	if len(msgs) == 0 {
		return ""
	}
	return strings.Join(msgs, "; ") + " — pick other gateway ports or use external mode with that proxy"
}

func (n *nativeProvisioner) isActive(ctx context.Context) bool {
	out, err := systemctl(ctx, "is-active", nativeUnitName)
	return err == nil && strings.TrimSpace(out) == "active"
}

// renderStatic emits Traefik's static configuration. Entrypoints bind the
// configured host ports directly (no container port mapping on native).
func (n *nativeProvisioner) renderStatic(gw setup.GatewayProvision) []byte {
	httpPort, httpsPort := gw.HTTPPort, gw.HTTPSPort
	if httpPort <= 0 {
		httpPort = 80
	}
	if httpsPort <= 0 {
		httpsPort = 443
	}
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteString("\n") }
	w("# Generated by node-stats (gateway) — do NOT edit by hand; changes are overwritten.")
	w("entryPoints:")
	w(fmt.Sprintf("  web:\n    address: \":%d\"", httpPort))
	w(fmt.Sprintf("  websecure:\n    address: \":%d\"", httpsPort))
	w("  ping:\n    address: \"127.0.0.1:8082\"")
	w("ping:\n  entryPoint: ping")
	w("providers:\n  file:")
	w(fmt.Sprintf("    directory: %q", n.dynamicDir()))
	w("    watch: true")
	w("api:\n  dashboard: false")
	w("log:\n  level: " + envOr("NODE_STATS_TRAEFIK_LOG_LEVEL", "INFO"))
	w("accessLog:")
	w(fmt.Sprintf("  filePath: %q", filepath.Join(n.dir(), "logs", "access.log")))
	w("  format: json")
	w("  fields:\n    headers:\n      names:\n        User-Agent: keep")
	w("global:\n  sendAnonymousUsage: false\n  checkNewVersion: false")
	if gw.ACMEEnabled {
		w("certificatesResolvers:\n  le:\n    acme:")
		w(fmt.Sprintf("      email: %q", gw.ACMEEmail))
		w(fmt.Sprintf("      storage: %q", filepath.Join(n.acmeDir(), "acme.json")))
		w("      httpChallenge:\n        entryPoint: web")
		if gw.ACMEStaging {
			w("      caServer: https://acme-staging-v02.api.letsencrypt.org/directory")
		}
	}
	return []byte(b.String())
}

func (n *nativeProvisioner) renderUnit() []byte {
	return []byte(fmt.Sprintf(`# Generated by node-stats (gateway) — do NOT edit by hand; changes are overwritten.
[Unit]
Description=Traefik gateway managed by node-stats
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s --configfile=%s
WorkingDirectory=%s
Restart=always
RestartSec=3
LimitNOFILE=65536
# Hardening (the binary still runs as root to bind :80/:443 and read the
# node-stats data dir; tighten further with AmbientCapabilities + a user if
# your setup allows).
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
`, n.binPath(), n.staticPath(), n.dir()))
}

// ensureBinary downloads the Traefik release tarball for this arch (checksum-
// verified against the release's checksums file) when the binary is missing.
func (n *nativeProvisioner) ensureBinary(ctx context.Context) error {
	if fi, err := os.Stat(n.binPath()); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		return nil
	}
	ver := n.resolveVersion(ctx)
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	case "arm":
		arch = "armv7"
	default:
		return fmt.Errorf("unsupported architecture %s", arch)
	}
	base := fmt.Sprintf("https://github.com/traefik/traefik/releases/download/%s/", ver)
	tarName := fmt.Sprintf("traefik_%s_linux_%s.tar.gz", ver, arch)
	n.logger.Info("gateway: downloading traefik", "version", ver, "arch", arch)

	tgz, err := httpGet(ctx, base+tarName, 2*time.Minute)
	if err != nil {
		return err
	}
	// Verify against the published checksums (best-effort fetch, strict compare).
	if sums, err := httpGet(ctx, base+fmt.Sprintf("traefik_%s_checksums.txt", ver), 30*time.Second); err == nil {
		want := ""
		for _, line := range strings.Split(string(sums), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] == tarName {
				want = f[0]
			}
		}
		if want != "" {
			sum := sha256.Sum256(tgz)
			if got := hex.EncodeToString(sum[:]); got != want {
				return fmt.Errorf("checksum mismatch for %s", tarName)
			}
		}
	}
	bin, err := extractFromTarGz(tgz, "traefik")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(n.binPath()), 0o755); err != nil {
		return err
	}
	tmp := n.binPath() + ".tmp"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, n.binPath()); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(n.binPath()), "VERSION"), []byte(ver+"\n"), 0o644)
	n.logger.Info("gateway: traefik installed", "path", n.binPath(), "version", ver)
	return nil
}

// resolveVersion picks NODE_STATS_TRAEFIK_VERSION, else the newest stable v3
// release from GitHub, else the pinned fallback.
func (n *nativeProvisioner) resolveVersion(ctx context.Context) string {
	if v := strings.TrimSpace(os.Getenv("NODE_STATS_TRAEFIK_VERSION")); v != "" {
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		return v
	}
	body, err := httpGet(ctx, "https://api.github.com/repos/traefik/traefik/releases?per_page=30", 15*time.Second)
	if err == nil {
		var rels []struct {
			TagName    string `json:"tag_name"`
			Prerelease bool   `json:"prerelease"`
			Draft      bool   `json:"draft"`
		}
		if json.Unmarshal(body, &rels) == nil {
			for _, r := range rels {
				if !r.Prerelease && !r.Draft && strings.HasPrefix(r.TagName, "v3.") {
					return r.TagName
				}
			}
		}
	}
	return nativeTraefikFallbackVersion
}

func extractFromTarGz(tgz []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, 512<<20))
		}
	}
	return nil, errors.New("traefik binary not found in archive")
}

func httpGet(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	rc, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rc, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "node-stats-gateway")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	rc, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(rc, "systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// writeIfChangedReport is writeIfChanged that also reports whether it wrote.
func writeIfChangedReport(path string, content []byte) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, content) {
		return false, nil
	}
	return true, writeIfChanged(path, content)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
