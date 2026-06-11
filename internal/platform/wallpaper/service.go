package wallpaper

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	connectors "system-stats/internal/platform/connectors"
)

const (
	// RotateSeconds is how often clients switch to the next photo. The index
	// is the wall-clock bucket, so every browser shows the same image.
	RotateSeconds = 300
	// cacheTTL bounds upstream searches: one per hour per mode covers any
	// number of browsers. The page cycles hourly so images don't repeat the
	// same 80-photo loop forever.
	cacheTTL = time.Hour
	perPage  = 80
	// pagesCycled spreads consecutive hours over this many result pages.
	pagesCycled = 5
)

// Wallpaper is the per-rotation payload served to browsers. Pexels guidelines
// ask for visible attribution — the frontend renders photographer + Pexels
// links from these fields.
type Wallpaper struct {
	URL             string `json:"url"`
	Photographer    string `json:"photographer"`
	PhotographerURL string `json:"photographer_url"`
	SourceURL       string `json:"source_url"`
	AvgColor        string `json:"avg_color"`
	RotateSeconds   int    `json:"rotate_seconds"`
}

type modeCache struct {
	photos    []Photo
	query     string
	page      int
	fetchedAt time.Time
}

// Service resolves the current wallpaper for a UI mode (dark/light).
type Service struct {
	logger *log.Logger
	repo   connectors.Repository
	cipher *connectors.Cipher
	client *Client

	mu    sync.Mutex
	cache map[string]*modeCache
	// now is stubbed in tests.
	now func() time.Time
}

// NewService wires the wallpaper proxy.
func NewService(logger *log.Logger, repo connectors.Repository, cipher *connectors.Cipher, client *Client) *Service {
	return &Service{
		logger: logger,
		repo:   repo,
		cipher: cipher,
		client: client,
		cache:  map[string]*modeCache{},
		now:    time.Now,
	}
}

// connectorQuery extracts the configured search query.
func connectorQuery(conn *connectors.Connector) string {
	var cfg struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal([]byte(conn.Config), &cfg)
	if q := strings.TrimSpace(cfg.Query); q != "" {
		return q
	}
	return "abstract dark technology"
}

// Current returns the wallpaper for "dark" or "light" mode, or (nil, nil)
// when no enabled Pexels connector exists — the UI keeps its bundled art.
func (s *Service) Current(ctx context.Context, mode string) (*Wallpaper, error) {
	if mode != "light" {
		mode = "dark"
	}
	conns, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	var conn *connectors.Connector
	for i := range conns {
		if conns[i].Type == connectors.TypePexels && conns[i].Enabled {
			conn = &conns[i]
			break
		}
	}
	if conn == nil {
		return nil, nil
	}
	apiKey, err := s.cipher.Decrypt(conn.SecretEnc)
	if err != nil {
		return nil, err
	}
	query := connectorQuery(conn)

	now := s.now()
	// Cycle result pages hourly so long-running dashboards keep seeing fresh
	// photos; pages beyond the result set fall back to page 1 below.
	page := 1 + int(now.Unix()/int64(cacheTTL.Seconds()))%pagesCycled

	s.mu.Lock()
	defer s.mu.Unlock()
	mc := s.cache[mode]
	if mc == nil || mc.query != query || mc.page != page ||
		now.Sub(mc.fetchedAt) > cacheTTL || len(mc.photos) == 0 {
		photos, ferr := s.fetch(ctx, apiKey, query, mode, page)
		if ferr != nil {
			if mc == nil || len(mc.photos) == 0 {
				return nil, ferr
			}
			// Upstream hiccup: serve the stale set rather than dropping the
			// background; next call retries.
			s.logger.Warn("wallpaper: refresh failed, serving cached set", "error", ferr)
		} else {
			mc = &modeCache{photos: photos, query: query, page: page, fetchedAt: now}
			s.cache[mode] = mc
		}
	}
	if len(mc.photos) == 0 {
		return nil, nil
	}

	p := mc.photos[int(now.Unix()/RotateSeconds)%len(mc.photos)]
	return &Wallpaper{
		// Pexels originals accept imgix-style resize params: cap at 4K wide
		// and let their CDN compress — "max quality, sane bytes".
		URL:             p.Src.Original + "?auto=compress&cs=tinysrgb&w=3840",
		Photographer:    p.Photographer,
		PhotographerURL: p.PhotographerURL,
		SourceURL:       p.URL,
		AvgColor:        p.AvgColor,
		RotateSeconds:   RotateSeconds,
	}, nil
}

// fetch pulls one page of photos, narrowing to dark tones for the dark UI
// mode. Fallbacks: a themed query may have no black-toned results — retry
// without the color filter; a cycled page may be past the result set — retry
// page 1.
func (s *Service) fetch(ctx context.Context, apiKey, query, mode string, page int) ([]Photo, error) {
	color := ""
	if mode == "dark" {
		color = "black"
	}
	resp, err := s.client.Search(ctx, apiKey, query, color, page, perPage)
	if err == nil && len(resp.Photos) == 0 && page > 1 {
		resp, err = s.client.Search(ctx, apiKey, query, color, 1, perPage)
	}
	if err == nil && len(resp.Photos) == 0 && color != "" {
		resp, err = s.client.Search(ctx, apiKey, query, "", 1, perPage)
	}
	if err != nil {
		return nil, err
	}
	return resp.Photos, nil
}
