package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// manifestAccept asks the registry for both multi-arch indexes and single
// image manifests (Docker + OCI media types).
const manifestAccept = "application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.manifest.v1+json"

// remoteImageVersion resolves a human-readable version for the image the
// registry tag currently points to, by fetching the remote image config and
// reading its version label / *_VERSION env (same logic as the local image).
// Returns "" on any failure (private/unsupported registry, auth, etc.).
func remoteImageVersion(ctx context.Context, client *http.Client, ref string) string {
	host, repo, tag := parseImageRef(ref)
	if host == "" || repo == "" {
		return ""
	}
	base := "https://" + host + "/v2/" + repo
	token := registryToken(ctx, client, host, repo)

	manifest, err := registryGet(ctx, client, base+"/manifests/"+tag, manifestAccept, token)
	if err != nil {
		return ""
	}
	cfgDigest := configDigest(ctx, client, base, token, manifest)
	if cfgDigest == "" {
		return ""
	}
	blob, err := registryGet(ctx, client, base+"/blobs/"+cfgDigest, "", token)
	if err != nil {
		return ""
	}
	var cfg struct {
		Config struct {
			Env    []string          `json:"Env"`
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if json.Unmarshal(blob, &cfg) != nil {
		return ""
	}
	return resolveImageVersion(cfg.Config.Labels, cfg.Config.Env, ref)
}

// parseImageRef splits an image reference into registry host, repository and tag,
// applying Docker Hub defaults (registry-1.docker.io + library/ namespace).
func parseImageRef(ref string) (host, repo, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	tag = "latest"
	name := ref
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		tag = ref[colon+1:]
		name = ref[:colon]
	}

	host = "registry-1.docker.io"
	repo = name
	if firstSlash := strings.Index(name, "/"); firstSlash >= 0 {
		first := name[:firstSlash]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			host = first
			repo = name[firstSlash+1:]
		}
	}
	if host == "registry-1.docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return host, repo, tag
}

// registryToken performs the Bearer challenge flow to obtain an anonymous pull
// token for the repository. Returns "" when the registry needs no token.
func registryToken(ctx context.Context, client *http.Client, host, repo string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/v2/", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return ""
	}
	chal := resp.Header.Get("Www-Authenticate")
	if !strings.HasPrefix(chal, "Bearer ") {
		return ""
	}
	p := parseChallenge(chal[len("Bearer "):])
	realm := p["realm"]
	if realm == "" {
		return ""
	}
	u := realm + "?service=" + url.QueryEscape(p["service"]) +
		"&scope=" + url.QueryEscape("repository:"+repo+":pull")
	treq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	tresp, err := client.Do(treq)
	if err != nil {
		return ""
	}
	defer tresp.Body.Close()
	var tk struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.NewDecoder(io.LimitReader(tresp.Body, 1<<20)).Decode(&tk) != nil {
		return ""
	}
	if tk.Token != "" {
		return tk.Token
	}
	return tk.AccessToken
}

// parseChallenge parses key="value" pairs from a WWW-Authenticate Bearer header.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = strings.Trim(kv[1], `"`)
		}
	}
	return out
}

// tagListPageLimit bounds how many tag-list pages we fetch per image so a huge
// repository can't make the hourly update check unbounded.
const tagListPageLimit = 25

// tagListTimeout bounds a whole tag-list walk: a slow or rate-limited registry
// must not hold a UI request open.
const tagListTimeout = 20 * time.Second

// listRepoTags fetches ref's repository tag list via the registry v2 tags/list
// endpoint, following pagination Link headers. Best-effort: returns whatever it
// has gathered on any error (including an empty slice).
func listRepoTags(ctx context.Context, client *http.Client, ref string) []string {
	host, repo, _ := parseImageRef(ref)
	if host == "" || repo == "" {
		return nil
	}
	token := registryToken(ctx, client, host, repo)
	next := "https://" + host + "/v2/" + repo + "/tags/list?n=100"
	var tags []string
	for i := 0; i < tagListPageLimit && next != ""; i++ {
		body, link, err := registryGetWithLink(ctx, client, next, token)
		if err != nil {
			break
		}
		var page struct {
			Tags []string `json:"tags"`
		}
		if json.Unmarshal(body, &page) != nil {
			break
		}
		tags = append(tags, page.Tags...)
		next = nextLinkURL(link, host)
	}
	return tags
}

// registryGetWithLink performs a registry GET returning the body and the raw
// RFC5988 Link response header (used for tags/list pagination).
func registryGetWithLink(ctx context.Context, client *http.Client, urlStr, token string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("registry tags GET %s: status %d", urlStr, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.Header.Get("Link"), err
}

// nextLinkURL extracts the rel="next" target from a registry Link header,
// resolving a relative path against host. A Link header can carry several
// comma-separated links (e.g. rel="prev" AND rel="next"), so we locate the
// "next" segment specifically rather than grabbing the first <...> pair.
// Returns "" when there is no next page.
func nextLinkURL(link, host string) string {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		target := part[start+1 : end]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			return target
		}
		return "https://" + host + target
	}
	return ""
}

// tagVersion is a parsed semver-ish image tag: numeric core + a "variant"
// suffix (e.g. "-alpine", "-management-alpine"). comps is how many numeric
// components were present (1=major only, 2=major.minor, 3=major.minor.patch).
type tagVersion struct {
	major, minor, patch, comps int
	variant                    string
}

func (v tagVersion) greaterThan(o tagVersion) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	if v.minor != o.minor {
		return v.minor > o.minor
	}
	return v.patch > o.patch
}

// tagVersionRe captures an optional leading v, 1-3 dotted numbers, and a trailing
// variant. The variant must be empty or begin with a separator (- or +) — this
// rejects pre-releases like "1.2.3rc1" and calver-with-extra like "1.2.3.4", and
// keeps clean variants like "-alpine".
var tagVersionRe = regexp.MustCompile(`^[vV]?(\d+)(?:\.(\d+))?(?:\.(\d+))?(.*)$`)

// parseTagVersion parses a tag into a comparable tagVersion. ok is false for
// non-version tags (latest, stable, edge, sha-pinned, pre-releases, …).
func parseTagVersion(tag string) (tagVersion, bool) {
	m := tagVersionRe.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return tagVersion{}, false
	}
	variant := m[4]
	if variant != "" && variant[0] != '-' && variant[0] != '+' {
		return tagVersion{}, false
	}
	v := tagVersion{variant: variant, comps: 1}
	v.major, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		v.minor, _ = strconv.Atoi(m[2])
		v.comps = 2
	}
	if m[3] != "" {
		v.patch, _ = strconv.Atoi(m[3])
		v.comps = 3
	}
	return v, true
}

// newerVersions inspects the available tags of the same variant as currentTag and
// returns the newest tag with the SAME major (sameMajor) and the newest tag with
// a HIGHER major (higherMajor). sameMajor is only reported when currentTag pins a
// minor/patch (>=2 components) — a bare rolling major tag like "18" already tracks
// its newest point release via the digest check, so suggesting "18.1" would be
// redundant noise. higherMajor (e.g. 1.2.1 → 2.0.0) is always reported.
func newerVersions(currentTag string, tags []string) (sameMajor, higherMajor string) {
	cur, ok := parseTagVersion(currentTag)
	if !ok {
		return "", ""
	}
	var bestSame, bestHigher tagVersion
	var haveSame, haveHigher bool
	for _, t := range tags {
		v, ok := parseTagVersion(t)
		// Variant must match (same flavour: -alpine, -management-alpine, …),
		// case-insensitively, and the candidate must be strictly newer.
		if !ok || !strings.EqualFold(v.variant, cur.variant) || !v.greaterThan(cur) {
			continue
		}
		if v.major == cur.major {
			if !haveSame || v.greaterThan(bestSame) {
				bestSame, sameMajor, haveSame = v, t, true
			}
		} else if v.major > cur.major {
			if !haveHigher || v.greaterThan(bestHigher) {
				bestHigher, higherMajor, haveHigher = v, t, true
			}
		}
	}
	if cur.comps < 2 {
		sameMajor = "" // bare rolling major tag → digest check already covers point releases
	}
	return sameMajor, higherMajor
}

func registryGet(ctx context.Context, client *http.Client, urlStr, accept, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry GET %s: status %d", urlStr, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// configDigest returns the image config blob digest from a manifest, resolving
// a manifest list/index to its linux/amd64 (or first) sub-manifest.
func configDigest(ctx context.Context, client *http.Client, base, token string, manifest []byte) string {
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(manifest, &m) != nil {
		return ""
	}
	if m.Config.Digest != "" {
		return m.Config.Digest
	}
	if len(m.Manifests) == 0 {
		return ""
	}
	pick := m.Manifests[0].Digest
	for _, mm := range m.Manifests {
		if mm.Platform.OS == "linux" && mm.Platform.Architecture == "amd64" {
			pick = mm.Digest
			break
		}
	}
	sub, err := registryGet(ctx, client, base+"/manifests/"+pick, manifestAccept, token)
	if err != nil {
		return ""
	}
	var sm struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if json.Unmarshal(sub, &sm) != nil {
		return ""
	}
	return sm.Config.Digest
}

// ListImageTags returns the tags an image's registry repository publishes,
// newest-looking first where the tags parse as versions. It is the read-only
// half of the update detector, exported so the application-backup feature can
// offer a version picker instead of only "there is something newer".
//
// Returns nil on any registry error: a version list is a convenience, and a
// rate-limited registry must not fail the request that asked for it.
func ListImageTags(ctx context.Context, ref string) []string {
	tags := listRepoTags(ctx, &http.Client{Timeout: tagListTimeout}, ref)
	if len(tags) == 0 {
		return nil
	}
	// Order: parseable versions descending, then the rest alphabetically, so
	// the picker opens on plausible upgrades rather than on "alpine".
	var versioned, plain []string
	for _, t := range tags {
		if _, ok := parseTagVersion(t); ok {
			versioned = append(versioned, t)
		} else {
			plain = append(plain, t)
		}
	}
	sort.Slice(versioned, func(i, j int) bool {
		a, _ := parseTagVersion(versioned[i])
		b, _ := parseTagVersion(versioned[j])
		return a.greaterThan(b)
	})
	sort.Strings(plain)
	return append(versioned, plain...)
}
