package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Public reachability check — "can the internet reach my gateway ports?".
// node-stats has no probe of its own on the internet, so this asks
// check-host.net (a free multi-location TCP checker with a JSON API) to
// connect to <public ip>:<port> from a few of its nodes. Triggered by an admin
// button only — it hands the node's public IP + ports to a third party.

// publicIPServices are queried in parallel and the majority answer wins. One
// service is not enough: with domain-based VPN/proxy routing some destinations
// leave through a tunnel and others directly (observed: ipify/ipinfo → VPS IP,
// everything else → the real ISP address). check-host.net/ip is listed first
// so the detected address is consistent with the probe provider.
var publicIPServices = []string{
	"https://check-host.net/ip",
	"https://checkip.amazonaws.com",
	"https://icanhazip.com",
	"https://ifconfig.me/ip",
	"https://api4.ipify.org",
}

const (
	checkHostBase    = "https://check-host.net"
	checkHostNodes   = 3
	checkHostTimeout = 20 * time.Second
)

// PublicCheckResult is the POST /gateway/check-public response.
type PublicCheckResult struct {
	PublicIP string `json:"public_ip"`
	// Detected is true when PublicIP was auto-detected (this node's egress IP)
	// rather than supplied by the admin.
	Detected bool `json:"detected"`
	// Candidates lists what each IP-echo service answered (auto-detect only);
	// disagreement means the node's traffic leaves through different paths
	// (VPN / proxy with per-destination routing).
	Candidates map[string]string `json:"candidates,omitempty"`
	Ports      []PublicPortCheck `json:"ports"`
	Provider   string            `json:"provider"`
	Error      string            `json:"error,omitempty"`
}

// PublicPortCheck is one port's verdict across probe locations.
type PublicPortCheck struct {
	Port      int               `json:"port"`
	Reachable bool              `json:"reachable"` // majority of responding probes connected
	Probes    []PublicProbeNode `json:"probes"`
}

// PublicProbeNode is one probe location's result.
type PublicProbeNode struct {
	Node     string  `json:"node"`
	Location string  `json:"location,omitempty"`
	OK       bool    `json:"ok"`
	TimeMS   float64 `json:"time_ms,omitempty"`
	Error    string  `json:"error,omitempty"`
}

var publicHTTP = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

// PublicCheck runs the external probe for the given ports. target overrides
// the auto-detected public IP (an IP or a hostname) — the node's egress may go
// through a VPN/tunnel whose address is not where the gateway is published.
func PublicCheck(ctx context.Context, target string, ports []int) PublicCheckResult {
	res := PublicCheckResult{Provider: "check-host.net"}
	ip := strings.TrimSpace(target)
	res.Detected = false
	if ip == "" {
		var err error
		ip, res.Candidates, err = publicIPConsensus(ctx)
		if err != nil {
			res.Error = "could not determine this node's public IP: " + err.Error()
			return res
		}
		res.Detected = true
	} else if strings.ContainsAny(ip, " /?#") {
		res.Error = "target must be an IP address or hostname"
		return res
	}
	res.PublicIP = ip
	for _, p := range ports {
		pc, err := checkHostTCP(ctx, ip, p)
		if err != nil {
			pc = PublicPortCheck{Port: p, Probes: []PublicProbeNode{{Node: "check-host.net", Error: err.Error()}}}
		}
		res.Ports = append(res.Ports, pc)
	}
	return res
}

// publicIPConsensus asks every echo service in parallel and returns the most
// common answer (ties → the first service in publicIPServices order).
func publicIPConsensus(ctx context.Context) (string, map[string]string, error) {
	type ans struct{ svc, ip string }
	ch := make(chan ans, len(publicIPServices))
	for _, svc := range publicIPServices {
		go func(svc string) {
			b, err := getJSON(ctx, svc, "text/plain")
			ip := ""
			if err == nil {
				ip = strings.TrimSpace(string(b))
				if net.ParseIP(ip) == nil {
					ip = ""
				}
			}
			ch <- ans{svc, ip}
		}(svc)
	}
	got := map[string]string{}
	for range publicIPServices {
		a := <-ch
		if a.ip != "" {
			got[shortHost(a.svc)] = a.ip
		}
	}
	if len(got) == 0 {
		return "", nil, fmt.Errorf("no IP echo service answered")
	}
	count := map[string]int{}
	for _, ip := range got {
		count[ip]++
	}
	best, bestN := "", 0
	for _, svc := range publicIPServices {
		ip := got[shortHost(svc)]
		if ip != "" && count[ip] > bestN {
			best, bestN = ip, count[ip]
		}
	}
	return best, got, nil
}

func shortHost(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(u, '/'); i > 0 {
		u = u[:i]
	}
	return u
}

// checkHostTCP submits a check-tcp request and polls its result.
func checkHostTCP(ctx context.Context, ip string, port int) (PublicPortCheck, error) {
	out := PublicPortCheck{Port: port}
	b, err := getJSON(ctx, fmt.Sprintf("%s/check-tcp?host=%s:%d&max_nodes=%d", checkHostBase, ip, port, checkHostNodes), "application/json")
	if err != nil {
		return out, err
	}
	var req struct {
		OK        int                 `json:"ok"`
		RequestID string              `json:"request_id"`
		Nodes     map[string][]string `json:"nodes"`
		Error     string              `json:"error"`
	}
	if err := json.Unmarshal(b, &req); err != nil || req.OK != 1 || req.RequestID == "" {
		if req.Error != "" {
			return out, fmt.Errorf("check-host.net: %s", req.Error)
		}
		return out, fmt.Errorf("check-host.net: unexpected response")
	}
	loc := map[string]string{}
	for node, meta := range req.Nodes {
		// meta: [country_code, country, city, ip, asn]
		if len(meta) >= 3 {
			loc[node] = strings.TrimSpace(meta[1] + ", " + meta[2])
		}
	}

	deadline := time.Now().Add(checkHostTimeout)
	var raw map[string]json.RawMessage
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		rb, err := getJSON(ctx, checkHostBase+"/check-result/"+req.RequestID, "application/json")
		if err != nil {
			continue
		}
		if json.Unmarshal(rb, &raw) != nil {
			continue
		}
		pending := false
		for _, v := range raw {
			if string(v) == "null" {
				pending = true
				break
			}
		}
		if !pending && len(raw) > 0 {
			break
		}
	}
	okCount, answered := 0, 0
	for node, v := range raw {
		pn := PublicProbeNode{Node: node, Location: loc[node]}
		if string(v) == "null" {
			pn.Error = "no answer (timed out waiting for the probe)"
			out.Probes = append(out.Probes, pn)
			continue
		}
		// [{"time":0.05,"address":"1.2.3.4"}] on success, [{"error":"..."}] on failure.
		var arr []map[string]any
		if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
			answered++
			if e, has := arr[0]["error"]; has && e != nil {
				pn.Error = fmt.Sprint(e)
			} else {
				pn.OK = true
				okCount++
				if t, ok := arr[0]["time"].(float64); ok {
					pn.TimeMS = t * 1000
				}
			}
		} else {
			pn.Error = "unparseable probe result"
		}
		out.Probes = append(out.Probes, pn)
	}
	sort.Slice(out.Probes, func(i, j int) bool { return out.Probes[i].Node < out.Probes[j].Node })
	out.Reachable = answered > 0 && okCount*2 > answered
	return out, nil
}

func getJSON(ctx context.Context, url, accept string) ([]byte, error) {
	rc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rc, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "node-stats-gateway")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := publicHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var buf strings.Builder
	if _, err := ioCopyLimited(&buf, resp); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func ioCopyLimited(dst *strings.Builder, resp *http.Response) (int64, error) {
	b := make([]byte, 32*1024)
	var n int64
	for n < 1<<20 {
		k, err := resp.Body.Read(b)
		dst.Write(b[:k])
		n += int64(k)
		if err != nil {
			if err.Error() == "EOF" {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}
