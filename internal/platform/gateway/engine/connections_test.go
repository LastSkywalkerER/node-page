package engine

import (
	"fmt"
	"testing"
	"time"
)

func line(ip, host, path, router string, status int) []byte {
	return []byte(fmt.Sprintf(`{"ClientHost":%q,"RequestHost":%q,"RequestMethod":"GET","RequestPath":%q,"DownstreamStatus":%d,"RouterName":%q,"Duration":1500000,"entryPointName":"websecure","RequestScheme":"https","StartUTC":%q,"request_User-Agent":"curl/8"}`,
		ip, host, path, status, router, time.Now().UTC().Format(time.RFC3339Nano)))
}

func TestConnTrackerIngestAndSnapshot(t *testing.T) {
	tr := NewConnTracker(nil)
	tr.startedAt = time.Now().UTC()

	// Normal traffic on a route.
	for i := 0; i < 5; i++ {
		tr.ingestLine(line("10.1.1.1", "app.example.com", "/", "ns-aaa111@file", 200))
	}
	// A scanner: no-route 404s + a probe path.
	for i := 0; i < 6; i++ {
		tr.ingestLine(line("203.0.113.9", "1.2.3.4", "/", "", 404))
	}
	for i := 0; i < 6; i++ {
		tr.ingestLine(line("203.0.113.9", "1.2.3.4", "/wp-login.php", "", 404))
	}
	// Blocked client hitting the deny router.
	tr.ingestLine(line("198.51.100.5", "app.example.com", "/", "ns-blocklist-https@file", 403))
	// Internal entrypoints must be ignored.
	tr.ingestLine([]byte(`{"ClientHost":"127.0.0.1","entryPointName":"ping","DownstreamStatus":200}`))

	v := tr.Snapshot(10, 30)
	if !v.Available {
		t.Fatalf("snapshot unavailable: %s", v.Reason)
	}
	if v.Total != 18 {
		t.Fatalf("total = %d, want 18", v.Total)
	}
	if v.NoRoute != 12 {
		t.Fatalf("no_route = %d, want 12", v.NoRoute)
	}
	if v.BlockedN != 1 {
		t.Fatalf("blocked = %d, want 1", v.BlockedN)
	}
	if v.UniqueIPs != 3 {
		t.Fatalf("unique_ips = %d, want 3", v.UniqueIPs)
	}
	if len(v.Recent) != 18 || v.Recent[0].IP != "198.51.100.5" || !v.Recent[0].Blocked {
		t.Fatalf("recent feed wrong: %+v", v.Recent[0])
	}
	// The scanner must rank first with a high suspicion score.
	if v.Top[0].IP != "203.0.113.9" {
		t.Fatalf("top[0] = %s, want the scanner", v.Top[0].IP)
	}
	if v.Top[0].Suspicion < 60 {
		t.Fatalf("scanner suspicion = %d, want >= 60 (scanner path + error ratio + no-route)", v.Top[0].Suspicion)
	}
	if v.Top[0].ScannerHits == 0 || v.Top[0].NoRoute != 12 {
		t.Fatalf("scanner counters wrong: %+v", v.Top[0])
	}
	// Route id extraction.
	found := false
	for _, e := range v.Recent {
		if e.RouteID == "aaa111" {
			found = true
		}
	}
	if !found {
		t.Fatal("route id not extracted from router name")
	}
	// Minute series must contain all 18 requests in the current bucket.
	var sum uint32
	for _, p := range v.Minutes {
		sum += p.Total
	}
	if sum != 18 {
		t.Fatalf("minute series sum = %d, want 18", sum)
	}
}

// The per-IP map must stay bounded: pushing past maxIPs evicts about half.
func TestConnTrackerIPEviction(t *testing.T) {
	tr := NewConnTracker(nil)
	tr.startedAt = time.Now().UTC()
	for i := 0; i < maxIPs+10; i++ {
		ip := fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff)
		tr.ingestLine(line(ip, "x.example.com", "/", "ns-aaa111@file", 200))
	}
	if len(tr.ips) > maxIPs {
		t.Fatalf("ip map grew past cap: %d > %d", len(tr.ips), maxIPs)
	}
	if len(tr.ips) < maxIPs/3 {
		t.Fatalf("eviction too aggressive: %d left", len(tr.ips))
	}
}
