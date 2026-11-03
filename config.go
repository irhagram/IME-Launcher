package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Gate is one connection endpoint of the server.
type Gate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// DiscordLink is a Discord server shown as a join button.
type DiscordLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Config is persisted in %AppData%\IMELauncher\config.json.
type Config struct {
	Discords    []DiscordLink `json:"discords"`
	DownloadDir string        `json:"downloadDir"`
	FiveMDir    string        `json:"fivemDir"`  // FiveM Application Data (FiveM.app)
	CacheURL    string        `json:"cacheUrl"`  // CDN page that lists the current cache version
	MetricsID   string        `json:"metricsId"` // server id on 5metrics.dev
	// FiveMSettings are written to CitizenFX.ini [Game] before connecting.
	FiveMSettings []Setting `json:"fivemSettings"`
	Gates         []Gate    `json:"gates"`
}

func defaultConfig() Config {
	return Config{
		Discords: []DiscordLink{
			{Name: "IME Roleplay", URL: "https://discord.gg/imeroleplay"},
			{Name: "Motion IME Community", URL: "https://discord.gg/motionime"},
		},
		CacheURL:      "https://cdn.ferrenza.com/?lang=id",
		MetricsID:     "zrvmg4",
		FiveMSettings: defaultFiveMSettings(),
		Gates: []Gate{
			{ID: "main", Name: "Main Gate", Host: "main.imeroleplay.com", Port: 30120},
			{ID: "inter", Name: "Inter Gate", Host: "inter.imeroleplay.com", Port: 30120},
		},
	}
}

type configStore struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func newConfigStore(path string) *configStore {
	s := &configStore{path: path, cfg: defaultConfig()}
	if b, err := os.ReadFile(path); err == nil {
		var c Config
		if json.Unmarshal(b, &c) == nil {
			d := defaultConfig()
			if len(c.Discords) == 0 {
				c.Discords = d.Discords
			}
			// the old temporary invite expired; move saved configs to the permanent one
			for i := range c.Discords {
				if strings.Contains(c.Discords[i].URL, "J7jJ5w4F") {
					c.Discords[i].URL = d.Discords[0].URL
				}
			}
			if c.CacheURL == "" {
				c.CacheURL = d.CacheURL
			}
			if c.MetricsID == "" {
				c.MetricsID = d.MetricsID
			}
			if len(c.FiveMSettings) == 0 {
				c.FiveMSettings = d.FiveMSettings
			}
			if len(c.Gates) == 0 {
				c.Gates = d.Gates
			}
			s.cfg = c
		}
	}
	return s
}

func (s *configStore) get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *configStore) update(fn func(c *Config)) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	if b, err := json.MarshalIndent(s.cfg, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
		_ = os.WriteFile(s.path, b, 0o644)
	}
	return s.cfg
}
