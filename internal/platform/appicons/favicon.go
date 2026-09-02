package appicons

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Application favicons — the last icon fallback, fetched SERVER-SIDE.
//
// The browser used to load `<origin>/favicon.ico` itself for apps with no
// registry icon. From an https dashboard that is (a) mixed content the browser
// blocks and (b) a request into the private network, which Chrome ≥ 138 gates
// behind the "access other apps and services on this device" permission
// prompt. The node fetches instead — it is on the network the apps live on —
// and caches the bytes in the same DB store as the registry icons (key
// "fav:<origin>", hit/miss TTLs as above).
//
// SSRF guard: only origins the node itself derived for its applications
// (container public URLs / published ports / proxy routes) are fetched —
// the handler is given an allow predicate by the server wiring.

// OriginAllowed reports whether origin ("https://host[:port]") belongs to a
// known application on some host.
type OriginAllowed func(ctx context.Context, origin string) bool

const (
	faviconKeyPrefix   = "fav:"
	faviconFetchBudget = 6 * time.Second
	// DuckDuckGoIconsBase serves favicons by hostname (public names only).
	DuckDuckGoIconsBase = "https://icons.duckduckgo.com/ip3"
)

// FaviconHandler serves GET /api/v1/app-icons/favicon?origin=<scheme://host[:port]>.
func (s *Service) FaviconHandler(allowed OriginAllowed) gin.HandlerFunc {
	// Self-hosted apps behind an IP:port speak self-signed TLS more often than
	// not; the bytes are an icon, so verification buys nothing here.
	client := &http.Client{
		Timeout: faviconFetchBudget,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			MaxIdleConns:        4,
			IdleConnTimeout:     30 * time.Second,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 4 * time.Second,
		},
	}
	return func(c *gin.Context) {
		origin, ok := NormalizeOrigin(c.Query("origin"))
		if !ok {
			c.Header("Cache-Control", "public, max-age=3600")
			c.Status(http.StatusNotFound)
			return
		}
		if allowed == nil || !allowed(c.Request.Context(), origin) {
			// Unknown origin: never fetched (SSRF), cached as a miss client-side.
			c.Header("Cache-Control", "public, max-age=3600")
			c.Status(http.StatusNotFound)
			return
		}
		data, ctype, found := s.favicon(c.Request.Context(), client, origin)
		if !found {
			c.Header("Cache-Control", "public, max-age=3600")
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Data(http.StatusOK, ctype, data)
	}
}

// NormalizeOrigin reduces a URL to "scheme://host[:port]" (lower-cased host,
// default port dropped). ok=false for anything that is not http(s) with a host.
func NormalizeOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") { // bare IPv6
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, true
}

// favicon resolves (DB cache first) the icon for an allowed origin.
func (s *Service) favicon(ctx context.Context, client *http.Client, origin string) ([]byte, string, bool) {
	key := faviconKeyPrefix + origin
	if s.store != nil {
		if data, ctype, ok, exp, found := s.store.Load(ctx, key); found && time.Now().Before(exp) {
			return data, ctype, ok
		}
	}
	v, _, _ := s.sf.Do(key, func() (interface{}, error) {
		budgetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveBudget)
		defer cancel()
		var data []byte
		var ctype string
		ok := false
		for _, u := range faviconCandidates(origin) {
			d, ct, err := fetchImage(budgetCtx, client, u)
			if err == nil {
				data, ctype, ok = d, ct, true
				break
			}
		}
		ttl := hitTTL
		if !ok {
			ttl = missTTL
		}
		if s.store != nil {
			s.store.Save(context.WithoutCancel(ctx), key, data, ctype, ok, time.Now().Add(ttl))
		}
		return entry{data: data, ctype: ctype, ok: ok}, nil
	})
	res := v.(entry)
	return res.data, res.ctype, res.ok
}

// faviconCandidates: the app's own well-known icon paths, then (public
// hostnames only — an IP means nothing to it) DuckDuckGo's favicon service.
func faviconCandidates(origin string) []string {
	out := []string{origin + "/favicon.ico", origin + "/favicon.png", origin + "/apple-touch-icon.png"}
	if u, err := url.Parse(origin); err == nil {
		h := u.Hostname()
		if net.ParseIP(h) == nil && strings.Contains(h, ".") && !strings.HasSuffix(h, ".local") && h != "localhost" {
			out = append(out, DuckDuckGoIconsBase+"/"+h+".ico")
		}
	}
	return out
}

// fetchImage GETs u and accepts only image payloads (declared or sniffed).
func fetchImage(ctx context.Context, client *http.Client, u string) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, faviconFetchBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errStatus(resp.StatusCode)
	}
	data, err := readCapped(resp.Body, maxIconSize)
	if err != nil || len(data) == 0 {
		return nil, "", errEmpty
	}
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(ctype, "image/") {
		// Servers hand .ico out as application/octet-stream or text/plain; sniff.
		sniffed := http.DetectContentType(data)
		switch {
		case strings.HasPrefix(sniffed, "image/"):
			ctype = sniffed
		case len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0:
			ctype = "image/x-icon"
		case strings.HasPrefix(strings.TrimSpace(string(data[:min(len(data), 64)])), "<svg"), strings.Contains(string(data[:min(len(data), 256)]), "<svg"):
			ctype = "image/svg+xml"
		default:
			return nil, "", errEmpty // an HTML login page is not an icon
		}
	}
	return data, ctype, nil
}
