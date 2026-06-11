package proxmox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectors "system-stats/internal/platform/connectors"
)

// mockPVE serves the minimal /api2/json surface Probe touches. clustered
// toggles between a named cluster and a default standalone "pve" node.
func mockPVE(t *testing.T, clustered bool) *httptest.Server {
	t.Helper()
	status := []map[string]any{{"type": "node", "name": "pve", "nodeid": 1}}
	if clustered {
		status = append(status, map[string]any{"type": "cluster", "name": "homelab"})
	}
	data := map[string]any{
		"/version":        map[string]any{"version": "8.2.4"},
		"/cluster/status": status,
		"/cluster/resources": []map[string]any{
			{"type": "node", "node": "pve", "status": "online"},
			{"type": "qemu", "node": "pve", "vmid": 100, "name": "vm-a", "status": "running"},
			{"type": "qemu", "node": "pve", "vmid": 900, "name": "tpl", "status": "stopped", "template": 1},
		},
		"/nodes/pve/qemu/100/config": map[string]any{
			"ostype": "l26", "net0": "virtio=DE:AD:BE:EF:00:64,bridge=vmbr0",
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=monitor@pam!stats=good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := data[strings.TrimPrefix(r.URL.Path, "/api2/json")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
	}))
}

func TestProbeStandaloneFingerprintIsEndpointQualified(t *testing.T) {
	srv := mockPVE(t, false)
	defer srv.Close()

	res, err := NewProber().Probe(context.Background(), srv.URL, "monitor@pam!stats", "good", false)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// Default install name "pve" must not collide across two independent
	// standalone Proxmoxes — the endpoint host disambiguates.
	want := "node/pve@" + strings.TrimPrefix(srv.URL, "http://")
	if res.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", res.Fingerprint, want)
	}
	if res.GuestCount != 1 { // template excluded
		t.Errorf("guest_count = %d, want 1", res.GuestCount)
	}
	if len(res.GuestMACs) != 1 || res.GuestMACs[0] != "de:ad:be:ef:00:64" {
		t.Errorf("guest_macs = %v", res.GuestMACs)
	}
}

func TestProbeClusterFingerprintStaysEndpointAgnostic(t *testing.T) {
	srv := mockPVE(t, true)
	defer srv.Close()

	res, err := NewProber().Probe(context.Background(), srv.URL, "monitor@pam!stats", "good", false)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	// Entering the same cluster via any node's endpoint must dedupe to one
	// connector, so the host is NOT baked in.
	if res.Fingerprint != "cluster/homelab" {
		t.Errorf("fingerprint = %q, want %q", res.Fingerprint, "cluster/homelab")
	}
}

func TestProbeAuthFailure(t *testing.T) {
	srv := mockPVE(t, false)
	defer srv.Close()

	_, err := NewProber().Probe(context.Background(), srv.URL, "monitor@pam!stats", "WRONG", false)
	if !errors.Is(err, connectors.ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

// PVE signals "token valid but unprivileged" via the HTTP reason phrase
// ("403 Permission check failed (/, Sys.Audit)") — the Privilege Separation
// pitfall. Go's httptest cannot write a custom reason phrase, so a raw TCP
// listener plays the PVE side.
func TestProbePrivilegeSeparationHint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, rerr := br.ReadString('\n')
					if rerr != nil || line == "\r\n" {
						break
					}
				}
				_, _ = c.Write([]byte("HTTP/1.1 403 Permission check failed (/, Sys.Audit)\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
			}(conn)
		}
	}()

	_, err = NewProber().Probe(context.Background(), "http://"+ln.Addr().String(), "root@pam!node-stats", "secret", false)
	if !errors.Is(err, connectors.ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if !strings.Contains(err.Error(), "pveum acl modify / -token 'root@pam!node-stats' -role PVEAuditor") {
		t.Errorf("error lacks the actionable privsep hint: %v", err)
	}
}
