package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const metricsTTL = 2 * time.Minute

// MetricPoint is one 30-minute sample of the player-count history.
type MetricPoint struct {
	T int64 `json:"t"` // unix ms
	N int   `json:"n"`
}

// Metrics is what 5metrics.dev publishes for the server.
type Metrics struct {
	OK      bool          `json:"ok"`
	Players int           `json:"players"`
	Avg     int           `json:"avg"`
	Peak    int           `json:"peak"` // highest count in the 24h window
	Min     int           `json:"min"`
	Rank    int           `json:"rank"` // global server-list rank
	Series  []MetricPoint `json:"series"`
	Updated int64         `json:"updated"` // unix ms of the last sample
	Error   string        `json:"error,omitempty"`
}

var (
	metricsMu    sync.Mutex
	metricsCache Metrics
	metricsAt    time.Time

	reFlight = regexp.MustCompile(`(?s)self\.__next_f\.push\(\[1,("(?:[^"\\]|\\.)*")\]\)`)
)

// GetMetrics scrapes the public 5metrics server page (it has no JSON API; the data
// is embedded in the page's Next.js payload) and caches the result for a couple of minutes.
func (a *App) GetMetrics() Metrics {
	if !a.allowed() {
		return Metrics{Error: a.lockedMsg()}
	}
	metricsMu.Lock()
	defer metricsMu.Unlock()
	if metricsCache.OK && time.Since(metricsAt) < metricsTTL {
		return metricsCache
	}
	m, err := fetchMetrics(a.cfg.get().MetricsID)
	if err != nil {
		if metricsCache.OK { // keep showing the last good data
			return metricsCache
		}
		return Metrics{Error: err.Error()}
	}
	metricsCache, metricsAt = m, time.Now()
	return m
}

func fetchMetrics(id string) (Metrics, error) {
	if id == "" {
		return Metrics{}, errors.New("metricsId kosong")
	}
	req, _ := http.NewRequest("GET", "https://5metrics.dev/server/"+id, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Metrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Metrics{}, fmt.Errorf("5metrics HTTP %d", resp.StatusCode)
	}
	html, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Metrics{}, err
	}

	var payload strings.Builder
	for _, m := range reFlight.FindAllSubmatch(html, -1) {
		var chunk string
		if json.Unmarshal(m[1], &chunk) == nil {
			payload.WriteString(chunk)
		}
	}
	text := payload.String()
	at := strings.Index(text, `"server":{`)
	if at < 0 {
		return Metrics{}, errors.New("format halaman 5metrics berubah")
	}
	var wrap struct {
		Players      int `json:"players"`
		AvgPlayers   int `json:"avgPlayers"`
		MaxPlayers   int `json:"maxPlayers"`
		MinPlayers   int `json:"minPlayers"`
		Rank         int `json:"rank"`
		PlayerCounts []struct {
			N int    `json:"playerCount"`
			T string `json:"timestmap"` // sic: spelled like this by 5metrics
		} `json:"playerCounts"`
	}
	dec := json.NewDecoder(strings.NewReader(text[at+len(`"server":`):]))
	if err := dec.Decode(&wrap); err != nil {
		return Metrics{}, fmt.Errorf("gagal membaca data 5metrics: %w", err)
	}

	out := Metrics{OK: true, Players: wrap.Players, Avg: wrap.AvgPlayers, Peak: wrap.MaxPlayers, Min: wrap.MinPlayers, Rank: wrap.Rank}
	for _, p := range wrap.PlayerCounts {
		if t, err := time.Parse(time.RFC3339Nano, p.T); err == nil {
			out.Series = append(out.Series, MetricPoint{T: t.UnixMilli(), N: p.N})
		}
	}
	if n := len(out.Series); n > 0 {
		out.Updated = out.Series[n-1].T
	}
	return out, nil
}
