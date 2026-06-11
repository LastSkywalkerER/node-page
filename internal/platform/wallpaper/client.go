// Package wallpaper serves dynamic dashboard backgrounds from the Pexels API
// through a backend proxy: the API key (stored encrypted on the Pexels
// connector, see internal/platform/connectors) never reaches the browser, and
// one cached upstream search per hour serves every connected client — Pexels'
// 200 req/h free tier is never a concern. Browsers poll GET /api/v1/wallpaper
// every rotation period; the photo index is derived from the wall clock, so
// all clients rotate to the same image at the same time.
package wallpaper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	connectors "system-stats/internal/platform/connectors"
)

// Photo is the subset of a Pexels photo we consume.
type Photo struct {
	ID              int    `json:"id"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	URL             string `json:"url"` // pexels.com photo page (attribution link)
	Photographer    string `json:"photographer"`
	PhotographerURL string `json:"photographer_url"`
	AvgColor        string `json:"avg_color"`
	Src             struct {
		Original string `json:"original"`
	} `json:"src"`
}

type searchResponse struct {
	Photos       []Photo `json:"photos"`
	TotalResults int     `json:"total_results"`
}

// AuthError marks a rejected API key.
type AuthError struct{ Status string }

func (e *AuthError) Error() string { return "pexels: authentication failed (" + e.Status + ")" }

// Client is a minimal Pexels API client.
type Client struct {
	base string
	http *http.Client
}

// NewClient talks to api.pexels.com; base is overridable for tests.
func NewClient(base string) *Client {
	if base == "" {
		base = "https://api.pexels.com"
	}
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

// Search queries landscape photos of at least ~12MP (size=medium guarantees
// ≳4000px wide originals — comfortably 4K). color narrows the palette; Pexels
// supports e.g. "black" for dark-theme backgrounds.
func (c *Client) Search(ctx context.Context, apiKey, query, color string, page, perPage int) (*searchResponse, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("orientation", "landscape")
	q.Set("size", "medium")
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(perPage))
	if color != "" {
		q.Set("color", color)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pexels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{Status: resp.Status}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("pexels: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("pexels: decode: %w", err)
	}
	return &out, nil
}

// ProbeKey implements connectors.PexelsProber: the cheapest authenticated call.
func (c *Client) ProbeKey(ctx context.Context, apiKey string) error {
	_, err := c.Search(ctx, apiKey, "nature", "", 1, 1)
	var ae *AuthError
	if errors.As(err, &ae) {
		return fmt.Errorf("%w: the Pexels API rejected this key (%s)", connectors.ErrAuthFailed, ae.Status)
	}
	return err
}
