package wallpaper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	connectors "system-stats/internal/platform/connectors"
)

type stubRepo struct {
	connectors.Repository
	rows []connectors.Connector
}

func (s *stubRepo) List(_ context.Context) ([]connectors.Connector, error) { return s.rows, nil }

func newTestService(t *testing.T, handler http.HandlerFunc, rows []connectors.Connector) (*Service, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc := NewService(log.New(nil), &stubRepo{rows: rows}, connectors.NewCipher("test-secret"), NewClient(srv.URL))
	return svc, srv
}

func pexelsRow(t *testing.T, query string, enabled bool) connectors.Connector {
	t.Helper()
	enc, err := connectors.NewCipher("test-secret").Encrypt("good-key")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"query": query})
	return connectors.Connector{
		ID: 1, Type: connectors.TypePexels, Fingerprint: connectors.PexelsFingerprint,
		SecretEnc: enc, Config: string(cfg), Enabled: enabled,
	}
}

func photosJSON(urls ...string) string {
	type photo struct {
		Photographer string `json:"photographer"`
		URL          string `json:"url"`
		Src          struct {
			Original string `json:"original"`
		} `json:"src"`
	}
	out := struct {
		Photos       []photo `json:"photos"`
		TotalResults int     `json:"total_results"`
	}{TotalResults: len(urls)}
	for _, u := range urls {
		p := photo{Photographer: "Ann", URL: "https://pexels.com/p"}
		p.Src.Original = u
		out.Photos = append(out.Photos, p)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func TestCurrentNoConnector(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called without a connector")
	}, nil)
	w, err := svc.Current(context.Background(), "dark")
	if err != nil || w != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", w, err)
	}
}

func TestCurrentDarkModeRequestsBlackTonesAndRotates(t *testing.T) {
	var sawColor atomic.Value
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "good-key" {
			w.WriteHeader(401)
			return
		}
		sawColor.Store(r.URL.Query().Get("color"))
		_, _ = w.Write([]byte(photosJSON("https://img/a.jpg", "https://img/b.jpg")))
	}, []connectors.Connector{pexelsRow(t, "cyberpunk", true)})

	base := time.Unix(1_000_000_000, 0) // bucket-aligned via integer division either way
	svc.now = func() time.Time { return base }
	w1, err := svc.Current(context.Background(), "dark")
	if err != nil || w1 == nil {
		t.Fatalf("current: %v %v", w1, err)
	}
	if sawColor.Load() != "black" {
		t.Errorf("dark mode must request color=black, got %q", sawColor.Load())
	}
	if !strings.Contains(w1.URL, "w=3840") {
		t.Errorf("url must cap width at 4K: %s", w1.URL)
	}
	if w1.RotateSeconds != RotateSeconds || w1.Photographer != "Ann" {
		t.Errorf("payload: %+v", w1)
	}

	// Same 5-minute bucket → same photo; next bucket → the other one.
	svc.now = func() time.Time { return base.Add(2 * time.Minute) }
	w2, _ := svc.Current(context.Background(), "dark")
	if w2.URL != w1.URL {
		t.Error("photo changed within one rotation bucket")
	}
	svc.now = func() time.Time { return base.Add(5 * time.Minute) }
	w3, _ := svc.Current(context.Background(), "dark")
	if w3.URL == w1.URL {
		t.Error("photo did not rotate on the next bucket")
	}
}

func TestCurrentLightModeNoColorFilter(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if c := r.URL.Query().Get("color"); c != "" {
			t.Errorf("light mode must not filter color, got %q", c)
		}
		_, _ = w.Write([]byte(photosJSON("https://img/c.jpg")))
	}, []connectors.Connector{pexelsRow(t, "abstract", true)})
	if w, err := svc.Current(context.Background(), "light"); err != nil || w == nil {
		t.Fatalf("light: %v %v", w, err)
	}
}

func TestCurrentDisabledConnector(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled connector must not hit upstream")
	}, []connectors.Connector{pexelsRow(t, "x", false)})
	if w, err := svc.Current(context.Background(), "dark"); err != nil || w != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", w, err)
	}
}

// A fresh upstream failure with no cached set is cached for failureTTL: the
// first call returns the raw error, subsequent calls short-circuit with
// ErrCachedFailure WITHOUT hitting upstream again (no 502 storm).
func TestCurrentCachesFailureAndStopsUpstreamCalls(t *testing.T) {
	var hits atomic.Int64
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}, []connectors.Connector{pexelsRow(t, "x", true)})

	base := time.Now()
	svc.now = func() time.Time { return base }
	if _, err := svc.Current(context.Background(), "dark"); err == nil {
		t.Fatal("first call should return the upstream error")
	}
	after := hits.Load()
	if after == 0 {
		t.Fatal("first call should reach upstream")
	}
	// Within failureTTL: short-circuited, upstream untouched.
	svc.now = func() time.Time { return base.Add(5 * time.Minute) }
	if _, err := svc.Current(context.Background(), "dark"); err != ErrCachedFailure {
		t.Fatalf("expected ErrCachedFailure, got %v", err)
	}
	if hits.Load() != after {
		t.Fatalf("cached failure must not hit upstream (%d -> %d)", after, hits.Load())
	}
	// After failureTTL expires the upstream is retried.
	svc.now = func() time.Time { return base.Add(failureTTL + time.Minute) }
	if _, err := svc.Current(context.Background(), "dark"); err == ErrCachedFailure {
		t.Fatal("expired failure must retry upstream, not short-circuit")
	}
	if hits.Load() == after {
		t.Fatal("expired failure should reach upstream again")
	}
}

// A bad decryption key (changed JWT secret) is cached too: it won't fix itself
// per request, so the second call short-circuits without re-attempting decrypt.
func TestCurrentCachesDecryptFailure(t *testing.T) {
	// Encrypt the key under a DIFFERENT secret than the service's cipher so
	// Decrypt fails with "message authentication failed".
	enc, err := connectors.NewCipher("other-secret").Encrypt("good-key")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"query": "x"})
	row := connectors.Connector{
		ID: 1, Type: connectors.TypePexels, Fingerprint: connectors.PexelsFingerprint,
		SecretEnc: enc, Config: string(cfg), Enabled: true,
	}
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be reached when decrypt fails")
	}, []connectors.Connector{row})

	base := time.Now()
	svc.now = func() time.Time { return base }
	if _, err := svc.Current(context.Background(), "dark"); err == nil {
		t.Fatal("decrypt failure should error")
	}
	svc.now = func() time.Time { return base.Add(time.Minute) }
	if _, err := svc.Current(context.Background(), "dark"); err != ErrCachedFailure {
		t.Fatalf("expected ErrCachedFailure on cached decrypt failure, got %v", err)
	}
}

func TestCurrentServesStaleOnUpstreamError(t *testing.T) {
	var failing atomic.Bool
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(photosJSON("https://img/keep.jpg")))
	}, []connectors.Connector{pexelsRow(t, "x", true)})

	base := time.Now()
	svc.now = func() time.Time { return base }
	if _, err := svc.Current(context.Background(), "dark"); err != nil {
		t.Fatalf("prime: %v", err)
	}
	failing.Store(true)
	svc.now = func() time.Time { return base.Add(2 * time.Hour) } // cache stale → refetch fails
	w, err := svc.Current(context.Background(), "dark")
	if err != nil || w == nil || !strings.Contains(w.URL, "keep.jpg") {
		t.Fatalf("stale fallback: %v %v", w, err)
	}
}
