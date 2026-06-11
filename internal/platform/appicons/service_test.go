package appicons

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func setup(t *testing.T) (svc *Service, cdnHits *int64) {
	t.Helper()
	var hits int64

	// Fake selfh.st CDN: only nginx-proxy-manager.svg and netbird.svg exist.
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		switch r.URL.Path {
		case "/svg/nginx-proxy-manager.svg", "/svg/netbird.svg", "/svg/crafty-controller.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte("<svg/>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cdn.Close)

	// Fake jsDelivr flat index for the selfh.st repo.
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[
			{"name":"/svg/nginx-proxy-manager.svg"},
			{"name":"/svg/netbird.svg"},
			{"name":"/svg/crafty-controller.svg"},
			{"name":"/png/nginx-proxy-manager.png"}
		]}`))
	}))
	t.Cleanup(index.Close)

	oldSelf, oldDash, oldIdx := SelfhstBase, DashboardIconsBase, SelfhstIndexURL
	SelfhstBase = cdn.URL
	DashboardIconsBase = cdn.URL + "/dash" // nothing matches there
	SelfhstIndexURL = index.URL
	t.Cleanup(func() { SelfhstBase, DashboardIconsBase, SelfhstIndexURL = oldSelf, oldDash, oldIdx })

	return NewService(nil), &hits
}

// "nginxproxymanager" (compose project name) must match the registry slug
// "nginx-proxy-manager" via collapsed-name index lookup.
func TestCollapsedNameMatch(t *testing.T) {
	svc, _ := setup(t)
	data, ctype, ok := svc.Get(context.Background(), "nginxproxymanager")
	if !ok {
		t.Fatal("expected a hit for nginxproxymanager")
	}
	if ctype != "image/svg+xml" || string(data) != "<svg/>" {
		t.Fatalf("got %q %q", ctype, data)
	}
}

// "crafty" should reach "crafty-controller" through the unique-prefix match.
func TestUniquePrefixMatch(t *testing.T) {
	svc, _ := setup(t)
	if _, _, ok := svc.Get(context.Background(), "crafty"); !ok {
		t.Fatal("expected a hit for crafty via prefix match")
	}
}

func TestCacheServesSecondRequestWithoutCDN(t *testing.T) {
	svc, hits := setup(t)
	if _, _, ok := svc.Get(context.Background(), "netbird"); !ok {
		t.Fatal("first request must hit")
	}
	before := atomic.LoadInt64(hits)
	if _, _, ok := svc.Get(context.Background(), "netbird"); !ok {
		t.Fatal("second request must hit")
	}
	if after := atomic.LoadInt64(hits); after != before {
		t.Fatalf("second request reached the CDN (%d -> %d)", before, after)
	}
}

func TestMissIsCachedNegatively(t *testing.T) {
	svc, hits := setup(t)
	if _, _, ok := svc.Get(context.Background(), "no-such-app-xyz"); ok {
		t.Fatal("expected a miss")
	}
	before := atomic.LoadInt64(hits)
	if _, _, ok := svc.Get(context.Background(), "no-such-app-xyz"); ok {
		t.Fatal("expected a cached miss")
	}
	if after := atomic.LoadInt64(hits); after != before {
		t.Fatal("negative result was not cached")
	}
}
