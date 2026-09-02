package appicons

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

func TestFaviconHandler_AllowListAndSniffing(t *testing.T) {
	ico := []byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 0, 0}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favicon.ico":
			w.Header().Set("Content-Type", "application/octet-stream") // sniffed as .ico
			_, _ = w.Write(ico)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>login</html>"))
		}
	}))
	defer app.Close()
	htmlOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an icon</html>"))
	}))
	defer htmlOnly.Close()

	svc := NewService(log.New(nil))
	allowed := map[string]bool{app.URL: true, htmlOnly.URL: true}
	h := svc.FaviconHandler(func(_ context.Context, origin string) bool { return allowed[origin] })
	gin.SetMode(gin.TestMode)
	call := func(origin string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/app-icons/favicon?origin="+origin, nil)
		h(c)
		c.Writer.WriteHeaderNow() // gin defers c.Status() until the body is written
		return w
	}
	if w := call(app.URL + "/some/path"); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/x-icon" || w.Body.Len() != len(ico) {
		t.Fatalf("known origin: %d %q %d", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}
	if w := call("http://127.0.0.1:1"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown origin must be refused without fetching: %d", w.Code)
	}
	if w := call(htmlOnly.URL); w.Code != http.StatusNotFound {
		t.Fatalf("an HTML page is not an icon: %d", w.Code)
	}
	if w := call("ftp://x"); w.Code != http.StatusNotFound {
		t.Fatalf("bad scheme: %d", w.Code)
	}
	for in, want := range map[string]string{
		"https://Grafana.Example.com:443/x?y": "https://grafana.example.com",
		"http://10.0.0.5:3000/":               "http://10.0.0.5:3000",
		"http://10.0.0.5:80":                  "http://10.0.0.5",
	} {
		if got, ok := NormalizeOrigin(in); !ok || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q %v", in, got, ok)
		}
	}
	if c := faviconCandidates("http://10.0.0.5:3000"); len(c) != 3 || strings.Contains(strings.Join(c, " "), "duckduckgo") {
		t.Errorf("ip origin must not consult duckduckgo: %v", c)
	}
	if c := faviconCandidates("https://grafana.example.com"); len(c) != 4 || !strings.HasSuffix(c[3], "/grafana.example.com.ico") {
		t.Errorf("public hostname candidates: %v", c)
	}
}
