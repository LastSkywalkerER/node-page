// Package proxmox implements the Proxmox VE connector: a thin read-only API
// client plus the poller that feeds the host registry / metric pipeline (see
// internal/platform/connectors for the registry this plugs into).
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Proxmox VE API endpoint with an API token
// (Authorization: PVEAPIToken=user@realm!tokenid=secret). All calls are
// read-only — the recommended token role is PVEAuditor.
type Client struct {
	base    string
	tokenID string
	secret  string
	http    *http.Client
}

// NewClient builds a client for the given endpoint (e.g. https://pve:8006).
func NewClient(endpoint, tokenID, secret string, skipTLSVerify bool) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: invalid endpoint %q", endpoint)
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		// Bound idle keep-alive connections. The poller builds a client every
		// ~10s cycle; without an idle timeout, idle connections (and their
		// parked reader goroutines) from discarded transports never expire and
		// leak for the process lifetime. Callers should also defer Close().
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
	if skipTLSVerify {
		// PVE ships a self-signed certificate by default; the admin opts in
		// explicitly via the connector form.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		base:    u.String(),
		tokenID: tokenID,
		secret:  secret,
		http:    &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}, nil
}

// Close releases the idle keep-alive connections held by this client's
// transport. The poller creates a client per poll cycle and discards it, so it
// must Close() each one — otherwise the transports' idle connections and their
// parked goroutines accumulate for the process lifetime.
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// AuthError marks a 401/403 from the PVE API.
type AuthError struct{ Status string }

func (e *AuthError) Error() string { return "proxmox: authentication failed (" + e.Status + ")" }

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api2/json"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.Status}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("proxmox: GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("proxmox: GET %s: decode: %w", path, err)
	}
	return json.Unmarshal(envelope.Data, out)
}

// Version returns the PVE version string (also the cheapest auth check).
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// ClusterStatusItem is one row of /cluster/status (type cluster or node).
type ClusterStatusItem struct {
	Type   string `json:"type"` // "cluster" | "node"
	Name   string `json:"name"`
	NodeID int    `json:"nodeid,omitempty"`
	IP     string `json:"ip,omitempty"`
	Online int    `json:"online,omitempty"`
	Local  int    `json:"local,omitempty"`
}

// ClusterStatus returns cluster membership (a single standalone node yields
// one "node" row and no "cluster" row).
func (c *Client) ClusterStatus(ctx context.Context) ([]ClusterStatusItem, error) {
	var items []ClusterStatusItem
	err := c.get(ctx, "/cluster/status", &items)
	return items, err
}

// Fingerprint derives the connector-side identity from /cluster/status: the
// PVE cluster name, or "node/<name>" for a standalone node.
func Fingerprint(items []ClusterStatusItem) string {
	for _, it := range items {
		if it.Type == "cluster" && it.Name != "" {
			return "cluster/" + it.Name
		}
	}
	for _, it := range items {
		if it.Type == "node" && it.Name != "" {
			return "node/" + it.Name
		}
	}
	return ""
}

// Resource is one row of /cluster/resources — the single cheap call that
// carries the whole topology plus live usage for every node and guest.
type Resource struct {
	Type     string  `json:"type"` // "node" | "qemu" | "lxc" | "storage" | ...
	Node     string  `json:"node"`
	VMID     int     `json:"vmid,omitempty"`
	Name     string  `json:"name,omitempty"`
	Status   string  `json:"status,omitempty"` // node: online/offline; guest: running/stopped/paused
	Template int     `json:"template,omitempty"`
	CPU      float64 `json:"cpu,omitempty"`    // fraction of MaxCPU (0..1)
	MaxCPU   float64 `json:"maxcpu,omitempty"` // cores
	Mem      uint64  `json:"mem,omitempty"`
	MaxMem   uint64  `json:"maxmem,omitempty"`
	Disk     uint64  `json:"disk,omitempty"`
	MaxDisk  uint64  `json:"maxdisk,omitempty"`
	Uptime   int64   `json:"uptime,omitempty"`
	NetIn    uint64  `json:"netin,omitempty"`
	NetOut   uint64  `json:"netout,omitempty"`
}

// ClusterResources lists nodes + guests with live usage.
func (c *Client) ClusterResources(ctx context.Context) ([]Resource, error) {
	var items []Resource
	err := c.get(ctx, "/cluster/resources", &items)
	return items, err
}

// NodeStatus is the hypervisor detail from /nodes/{node}/status.
type NodeStatus struct {
	PVEVersion string `json:"pveversion"`
	KVersion   string `json:"kversion"`
	Uptime     int64  `json:"uptime"`
	LoadAvg    []any  `json:"loadavg"` // strings or numbers depending on PVE version
	CPUInfo    struct {
		Model   string  `json:"model"`
		CPUs    int     `json:"cpus"`
		Sockets int     `json:"sockets"`
		MHz     string  `json:"mhz"`
		UserHz  float64 `json:"user_hz"`
	} `json:"cpuinfo"`
	Memory struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
		Free  uint64 `json:"free"`
	} `json:"memory"`
	RootFS struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
		Free  uint64 `json:"free"`
	} `json:"rootfs"`
}

// NodeStatus fetches hypervisor details for one node.
func (c *Client) NodeStatus(ctx context.Context, node string) (*NodeStatus, error) {
	var st NodeStatus
	if err := c.get(ctx, "/nodes/"+url.PathEscape(node)+"/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// LoadAvgFloats coerces the version-dependent loadavg representation.
func (st *NodeStatus) LoadAvgFloats() [3]float64 {
	var out [3]float64
	for i := 0; i < len(st.LoadAvg) && i < 3; i++ {
		switch v := st.LoadAvg[i].(type) {
		case float64:
			out[i] = v
		case string:
			fmt.Sscanf(v, "%f", &out[i])
		}
	}
	return out
}

// NodeNetIface is one row of /nodes/{node}/network.
type NodeNetIface struct {
	Iface   string `json:"iface"`
	Type    string `json:"type"` // eth | bridge | bond | vlan | ...
	HWAddr  string `json:"hwaddr,omitempty"`
	Address string `json:"address,omitempty"` // bare IP (older PVE)
	// CIDR is the address/CIDR form newer PVE returns instead of/alongside
	// Address (e.g. "10.0.0.5/24"); the management IP often lives only here.
	CIDR    string `json:"cidr,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Active  int    `json:"active,omitempty"`
}

// NodeNetwork lists a node's network interfaces (used to identify the
// hypervisor host row by a real NIC MAC).
func (c *Client) NodeNetwork(ctx context.Context, node string) ([]NodeNetIface, error) {
	var items []NodeNetIface
	err := c.get(ctx, "/nodes/"+url.PathEscape(node)+"/network", &items)
	return items, err
}

// RRDPoint is one sample of /nodes/{node}/rrddata. Unlike the cumulative
// guest counters in /cluster/resources, the node's netin/netout here are
// RRD-averaged RATES in bytes/second. Pointers because trailing samples of
// the window may have null fields.
type RRDPoint struct {
	Time   int64    `json:"time"`
	NetIn  *float64 `json:"netin,omitempty"`
	NetOut *float64 `json:"netout,omitempty"`
}

// NodeRRDData returns the node's last-hour metric series (~1-min resolution).
// The freshest complete point feeds the hypervisor's network widget — the
// only place the PVE API exposes node-level traffic.
func (c *Client) NodeRRDData(ctx context.Context, node string) ([]RRDPoint, error) {
	var items []RRDPoint
	err := c.get(ctx, "/nodes/"+url.PathEscape(node)+"/rrddata?timeframe=hour", &items)
	return items, err
}

// GuestConfig fetches a guest's config (kind is "qemu" or "lxc"). Values are
// strings like `net0: "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"`.
func (c *Client) GuestConfig(ctx context.Context, node, kind string, vmid int) (map[string]any, error) {
	cfg := map[string]any{}
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", url.PathEscape(node), kind, vmid)
	if err := c.get(ctx, path, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LXCNetIface is one row of /nodes/{node}/lxc/{vmid}/interfaces — the running
// container's runtime interfaces, used to resolve a DHCP-assigned IP the static
// config (ip=dhcp) doesn't carry. Inet/Inet6 may include a /CIDR suffix.
type LXCNetIface struct {
	Name   string `json:"name"`
	HWAddr string `json:"hwaddr,omitempty"`
	Inet   string `json:"inet,omitempty"`
	Inet6  string `json:"inet6,omitempty"`
}

// LXCInterfaces lists a running container's runtime interfaces. Errors when the
// CT is stopped — callers treat that as "no IP".
func (c *Client) LXCInterfaces(ctx context.Context, node string, vmid int) ([]LXCNetIface, error) {
	var items []LXCNetIface
	path := fmt.Sprintf("/nodes/%s/lxc/%d/interfaces", url.PathEscape(node), vmid)
	err := c.get(ctx, path, &items)
	return items, err
}

// qemuAgentIface mirrors one entry of the QEMU guest-agent
// network-get-interfaces result.
type qemuAgentIface struct {
	Name        string `json:"name"`
	IPAddresses []struct {
		Type    string `json:"ip-address-type"` // "ipv4" | "ipv6"
		Address string `json:"ip-address"`
	} `json:"ip-addresses"`
}

// GuestAgentInterfaces queries a running QEMU guest's agent for its non-loopback
// IPv4 addresses. Requires qemu-guest-agent installed AND running in the guest;
// returns an error otherwise (the caller treats that as "no IP").
func (c *Client) GuestAgentInterfaces(ctx context.Context, node string, vmid int) ([]string, error) {
	var res struct {
		Result []qemuAgentIface `json:"result"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid)
	if err := c.get(ctx, path, &res); err != nil {
		return nil, err
	}
	var ips []string
	for _, nf := range res.Result {
		if strings.EqualFold(nf.Name, "lo") {
			continue
		}
		for _, a := range nf.IPAddresses {
			if !strings.EqualFold(a.Type, "ipv4") {
				continue
			}
			if ip := parseIPv4(a.Address); ip != "" && !net.ParseIP(ip).IsLoopback() {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

// qemu net device models whose key carries the MAC ("virtio=AA:BB:…").
var qemuNicModels = map[string]bool{
	"virtio": true, "e1000": true, "e1000e": true, "e1000-82540em": true,
	"e1000-82544gc": true, "e1000-82545em": true, "rtl8139": true,
	"vmxnet3": true, "ne2k_pci": true, "ne2k_isa": true, "i82551": true,
	"i82557b": true, "i82559er": true, "pcnet": true,
}

// ConfigMACs extracts every NIC MAC from a guest config (lowercased). QEMU
// encodes it as <model>=<MAC>, LXC as hwaddr=<MAC>.
func ConfigMACs(cfg map[string]any) []string {
	var macs []string
	for key, raw := range cfg {
		if !strings.HasPrefix(key, "net") {
			continue
		}
		val, ok := raw.(string)
		if !ok {
			continue
		}
		for _, part := range strings.Split(val, ",") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 {
				continue
			}
			k := strings.ToLower(kv[0])
			if k == "hwaddr" || k == "macaddr" || qemuNicModels[k] {
				if mac := normalizeMAC(kv[1]); mac != "" {
					macs = append(macs, mac)
				}
			}
		}
	}
	return macs
}

// ConfigIPv4 extracts a static IPv4 from a guest config's netN lines (LXC
// `ip=192.168.1.5/24`). Returns "" for dhcp/manual/unset and for QEMU (whose
// netN never carries an ip=). The CIDR suffix is stripped.
func ConfigIPv4(cfg map[string]any) string {
	for key, raw := range cfg {
		if !strings.HasPrefix(key, "net") {
			continue
		}
		val, ok := raw.(string)
		if !ok {
			continue
		}
		for _, part := range strings.Split(val, ",") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 || strings.ToLower(kv[0]) != "ip" {
				continue
			}
			if ip := parseIPv4(kv[1]); ip != "" {
				return ip
			}
		}
	}
	return ""
}

// parseIPv4 strips an optional /CIDR suffix and returns the address only if it
// is a valid IPv4 literal (rejects "dhcp", "manual", IPv6).
func parseIPv4(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.String()
}

// firstLXCIPv4 returns the first non-loopback IPv4 from a running container's
// runtime interfaces (LXCInterfaces).
func firstLXCIPv4(ifaces []LXCNetIface) string {
	for _, nf := range ifaces {
		if strings.EqualFold(nf.Name, "lo") {
			continue
		}
		if ip := parseIPv4(nf.Inet); ip != "" && !net.ParseIP(ip).IsLoopback() {
			return ip
		}
	}
	return ""
}

// SMBIOSUUID extracts the guest UUID from a QEMU config's `smbios1` value
// ("uuid=xxx[,base64=1,...]"; the uuid field itself is never base64-encoded).
// Lowercased to match the agent-collected product_uuid. "" for LXC (no
// smbios1) or when unset.
func SMBIOSUUID(cfg map[string]any) string {
	raw, ok := cfg["smbios1"].(string)
	if !ok {
		return ""
	}
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "uuid") {
			return strings.ToLower(strings.TrimSpace(kv[1]))
		}
	}
	return ""
}

func normalizeMAC(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 17 || strings.Count(s, ":") != 5 {
		return ""
	}
	return s
}

// OSTypeInfo maps a PVE ostype to the host-row OS fields.
// QEMU uses codes (l26, win11, …); LXC uses distro names (debian, alpine, …).
func OSTypeInfo(ostype string) (osName, platform, family string) {
	o := strings.ToLower(strings.TrimSpace(ostype))
	switch {
	case o == "":
		return "", "", ""
	case strings.HasPrefix(o, "w") && o != "wxp" || strings.HasPrefix(o, "win"):
		return "windows", o, "windows"
	case o == "wxp":
		return "windows", o, "windows"
	case o == "l24" || o == "l26" || o == "linux":
		return "linux", "linux", "linux"
	case o == "solaris", o == "other":
		return o, o, ""
	default:
		// LXC distro names map straight to platform (matches gopsutil values).
		return "linux", o, "linux"
	}
}
