package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"system-stats/internal/version"
)

// maxAsset caps the in-memory download size (release archives are tens of MB).
const maxAsset = 200 << 20 // 200 MiB

// updateNative downloads the latest release asset for this OS/arch, verifies its
// checksum, and replaces the running binary. The operator restarts to apply.
func (s *Service) updateNative(ctx context.Context) (string, error) {
	rel, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return "", err
	}
	if rel == nil {
		return "No releases published yet.", nil
	}
	cur := version.Get().Current
	if !newerAvailable(cur, rel.TagName) {
		return fmt.Sprintf("Already up to date (%s).", cur), nil
	}
	if err := s.selfReplace(ctx, rel); err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated %s → %s. Restart node-stats to run the new version.", cur, rel.TagName), nil
}

// selfReplace fetches + verifies the platform asset and swaps the executable.
func (s *Service) selfReplace(ctx context.Context, rel *ghRelease) error {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	assetName := assetBaseName(rel.TagName) + ext

	var assetURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			assetURL = a.URL
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if assetURL == "" {
		return fmt.Errorf("no release asset %q for %s/%s", assetName, runtime.GOOS, runtime.GOARCH)
	}

	data, err := s.download(ctx, assetURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", assetName, err)
	}

	// Verify checksum when SHA256SUMS is published.
	if sumsURL != "" {
		sums, serr := s.download(ctx, sumsURL)
		if serr == nil {
			want := lookupSum(string(sums), assetName)
			got := fmt.Sprintf("%x", sha256.Sum256(data))
			if want != "" && !strings.EqualFold(want, got) {
				return fmt.Errorf("checksum mismatch for %s (want %s, got %s)", assetName, want, got)
			}
		}
	}

	binName := "node-stats"
	if runtime.GOOS == "windows" {
		binName = "node-stats.exe"
	}
	bin, err := extractBinary(data, ext, binName)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return replaceExecutable(exe, bin)
}

// download fetches url with the patient asset client, retrying transient
// network failures (slow TLS handshakes / dropped connections on SBC/LXC links)
// with a short backoff. Returns the body or the last error.
func (s *Service) download(ctx context.Context, url string) ([]byte, error) {
	cl := s.dlClient
	if cl == nil {
		cl = s.client
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 { // backoff before a retry: 1s, 4s
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration((attempt-1)*(attempt-1)) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := cl.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("GET %s: %s", url, resp.Status)
			// Client errors (404 etc.) won't fix on retry — bail immediately.
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxAsset))
		resp.Body.Close()
		if rerr != nil {
			lastErr = rerr
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

// lookupSum returns the hex digest for name from a "HASH  name" SHA256SUMS body.
func lookupSum(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && path.Base(fields[1]) == name {
			return fields[0]
		}
	}
	return ""
}

// extractBinary pulls the named binary out of a .tar.gz or .zip archive.
func extractBinary(data []byte, ext, binName string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxAsset))
			}
		}
		return nil, fmt.Errorf("%s not found in archive", binName)
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if path.Base(hdr.Name) == binName {
			return io.ReadAll(io.LimitReader(tr, maxAsset))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// replaceExecutable atomically swaps the binary at exe with newBin. On unix the
// running process keeps its open inode, so a rename is safe; on Windows the
// running exe can't be overwritten, so we move it aside first.
func replaceExecutable(exe string, newBin []byte) error {
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, ".node-stats.new")
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("move current exe aside: %w", err)
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe) // best-effort rollback
			return fmt.Errorf("install new exe: %w", err)
		}
		return nil
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}
