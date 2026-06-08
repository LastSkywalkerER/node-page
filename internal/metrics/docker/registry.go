package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
