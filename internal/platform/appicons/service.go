package appicons

// Server-side application-icon resolver + cache. Browsers used to hit icon
// CDNs directly: every client re-resolved the same misses, and compose-style
// names ("nginxproxymanager") never matched registry slugs
// ("nginx-proxy-manager"). The backend resolves a raw application name —
// collapsed-name matching against the selfh.st icon index plus direct CDN
// candidates — fetches the first hit once, and serves it to every client
// with long browser-cache headers.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/singleflight"
)

// CDN bases — package vars so tests can point them at httptest servers.
var (
	DashboardIconsBase = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons"
	SelfhstBase        = "https://cdn.jsdelivr.net/gh/selfhst/icons"
	SimpleIconsBase    = "https://cdn.jsdelivr.net/gh/simple-icons/simple-icons/icons"
	// SelfhstIndexURL lists the selfh.st icon repo's files (jsDelivr data API)
	// so misspelled/collapsed names can be matched against real slugs.
	SelfhstIndexURL = "https://data.jsdelivr.com/v1/packages/gh/selfhst/icons@main?structure=flat"
)

const (
	hitTTL = 24 * time.Hour
	// missTTL: a name that resolves to nothing rarely starts resolving later —
	// cache the miss for a full day so a wall of unknown app names doesn't
	// re-run the (up to) 6-fetch resolution every hour.
	missTTL  = 24 * time.Hour
	indexTTL = 24 * time.Hour
	// resolveBudget bounds the TOTAL time spent resolving one name across all
	// sequential candidate fetches (each candidate still has its own 6s cap),
	// so a string of slow/unreachable CDNs can't pin a request for ~36s.
	resolveBudget = 8 * time.Second
	maxEntries    = 2048
	maxIconSize   = 1 << 20 // 1 MiB per icon is plenty
)

type entry struct {
	data  []byte
	ctype string
	ok    bool
	exp   time.Time
}

type Service struct {
	logger *log.Logger
	httpc  *http.Client
	// sf collapses concurrent resolutions of the same name into one: a fresh
	// dashboard with N tiles for the same app used to fire N identical
	// 6-fetch resolutions in parallel — now they share one.
	sf singleflight.Group

	mu       sync.Mutex
	cache    map[string]entry
	index    map[string]string // collapsed name -> canonical slug
	indexExp time.Time
}

func NewService(logger *log.Logger) *Service {
	return &Service{
		logger: logger,
		httpc:  &http.Client{Timeout: 6 * time.Second},
		cache:  make(map[string]entry),
	}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	for _, ext := range []string{".svg", ".png", ".webp", ".ico"} {
		s = strings.TrimSuffix(s, ext)
	}
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func collapse(raw string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(raw), "")
}

// Get resolves raw (an app name / slug / sh- / si- prefixed value) to icon
// bytes. ok=false means no candidate matched (cached negatively).
func (s *Service) Get(ctx context.Context, raw string) (data []byte, ctype string, ok bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return nil, "", false
	}
	s.mu.Lock()
	if e, hit := s.cache[key]; hit && time.Now().Before(e.exp) {
		s.mu.Unlock()
		return e.data, e.ctype, e.ok
	}
	s.mu.Unlock()

	// Collapse concurrent resolutions of the same name into one external pass;
	// every caller shares the single result. The budget context is owned by the
	// flight leader, so a cancelled follower can't abort the shared work.
	v, _, _ := s.sf.Do(key, func() (interface{}, error) {
		// Cap the TOTAL resolution time regardless of how many candidates the
		// fallback chain produces (each candidate fetch also has its own 6s cap).
		budgetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveBudget)
		defer cancel()
		rd, rc, rok := s.resolve(budgetCtx, key)
		ttl := hitTTL
		if !rok {
			ttl = missTTL
		}
		s.mu.Lock()
		if len(s.cache) >= maxEntries {
			for k := range s.cache {
				delete(s.cache, k)
				if len(s.cache) < maxEntries/2 {
					break
				}
			}
		}
		s.cache[key] = entry{data: rd, ctype: rc, ok: rok, exp: time.Now().Add(ttl)}
		s.mu.Unlock()
		return entry{data: rd, ctype: rc, ok: rok}, nil
	})
	res := v.(entry)
	return res.data, res.ctype, res.ok
}

func (s *Service) resolve(ctx context.Context, key string) ([]byte, string, bool) {
	for _, u := range s.candidates(ctx, key) {
		if data, ctype, err := s.fetch(ctx, u); err == nil {
			return data, ctype, true
		}
	}
	return nil, "", false
}

func (s *Service) candidates(ctx context.Context, key string) []string {
	if strings.HasPrefix(key, "sh-") {
		slug := normalizeSlug(key[3:])
		return []string{
			SelfhstBase + "/svg/" + slug + ".svg",
			SelfhstBase + "/png/" + slug + ".png",
		}
	}
	if strings.HasPrefix(key, "si-") {
		return []string{SimpleIconsBase + "/" + normalizeSlug(key[3:]) + ".svg"}
	}

	slug := normalizeSlug(key)
	if slug == "" {
		return nil
	}
	var out []string
	// Collapsed-name match against the live selfh.st index first: it is the
	// only candidate that can bridge "nginxproxymanager" → "nginx-proxy-manager".
	if canonical := s.lookupIndex(ctx, slug); canonical != "" && canonical != slug {
		out = append(out,
			SelfhstBase+"/svg/"+canonical+".svg",
			SelfhstBase+"/png/"+canonical+".png",
		)
	}
	out = append(out,
		DashboardIconsBase+"/svg/"+slug+".svg",
		SelfhstBase+"/svg/"+slug+".svg",
		DashboardIconsBase+"/webp/"+slug+".webp",
		SelfhstBase+"/png/"+slug+".png",
	)
	return out
}

// lookupIndex maps a collapsed app name to the canonical selfh.st slug:
// exact collapsed match first, then a unique prefix match ("crafty" →
// "crafty-controller"). Best-effort — an unreachable index disables only
// this candidate source.
func (s *Service) lookupIndex(ctx context.Context, slug string) string {
	idx := s.loadIndex(ctx)
	if len(idx) == 0 {
		return ""
	}
	q := collapse(slug)
	if canonical, hit := idx[q]; hit {
		return canonical
	}
	if len(q) >= 4 {
		match := ""
		for collapsed, canonical := range idx {
			if strings.HasPrefix(collapsed, q) {
				if match != "" && match != canonical {
					return "" // ambiguous
				}
				match = canonical
			}
		}
		return match
	}
	return ""
}

func (s *Service) loadIndex(ctx context.Context) map[string]string {
	s.mu.Lock()
	if s.index != nil && time.Now().Before(s.indexExp) {
		idx := s.index
		s.mu.Unlock()
		return idx
	}
	s.mu.Unlock()

	idx := s.fetchIndex(ctx)
	s.mu.Lock()
	// Keep a stale index on refresh failure rather than dropping matching.
	if idx != nil || s.index == nil {
		s.index = idx
	}
	s.indexExp = time.Now().Add(indexTTL)
	idxOut := s.index
	s.mu.Unlock()
	return idxOut
}

func (s *Service) fetchIndex(ctx context.Context) map[string]string {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, SelfhstIndexURL, nil)
	if err != nil {
		return nil
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("appicons: index fetch failed", "error", err)
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&payload); err != nil {
		return nil
	}
	idx := make(map[string]string)
	for _, f := range payload.Files {
		name := f.Name // e.g. "/svg/nginx-proxy-manager.svg"
		if !strings.HasPrefix(name, "/svg/") || !strings.HasSuffix(name, ".svg") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "/svg/"), ".svg")
		if slug == "" || strings.Contains(slug, "/") {
			continue
		}
		idx[collapse(slug)] = slug
	}
	if s.logger != nil {
		s.logger.Debug("appicons: index loaded", "slugs", len(idx))
	}
	return idx
}

func (s *Service) fetch(ctx context.Context, rawURL string) ([]byte, string, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, "", err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errStatus(resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIconSize))
	if err != nil || len(data) == 0 {
		return nil, "", errEmpty
	}
	ctype := resp.Header.Get("Content-Type")
	if ctype == "" || strings.HasPrefix(ctype, "text/plain") {
		switch {
		case strings.HasSuffix(rawURL, ".svg"):
			ctype = "image/svg+xml"
		case strings.HasSuffix(rawURL, ".webp"):
			ctype = "image/webp"
		default:
			ctype = "image/png"
		}
	}
	return data, ctype, nil
}

type statusErr int

func (e statusErr) Error() string { return "unexpected status " + http.StatusText(int(e)) }
func errStatus(code int) error    { return statusErr(code) }

var errEmpty = statusErr(http.StatusNoContent)
