package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const discordTTL = 5 * time.Minute

type DiscordInfo struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	OK      bool   `json:"ok"`
	Members int    `json:"members"`
	Online  int    `json:"online"`
	Icon    string `json:"icon"`
}

var (
	discordMu    sync.Mutex
	discordCache = map[string]DiscordInfo{}
	discordAt    = map[string]time.Time{}
)

// GetDiscords returns the configured Discord servers with member counts from the
// public invite endpoint (cached for a few minutes).
func (a *App) GetDiscords() []DiscordInfo {
	if !a.allowed() {
		return nil
	}
	links := a.cfg.get().Discords
	out := make([]DiscordInfo, len(links))
	var wg sync.WaitGroup
	for i, l := range links {
		wg.Add(1)
		go func(i int, l DiscordLink) {
			defer wg.Done()
			out[i] = discordInfo(l)
		}(i, l)
	}
	wg.Wait()
	return out
}

func discordInfo(l DiscordLink) DiscordInfo {
	discordMu.Lock()
	if c, ok := discordCache[l.URL]; ok && time.Since(discordAt[l.URL]) < discordTTL {
		discordMu.Unlock()
		c.Name = l.Name
		return c
	}
	discordMu.Unlock()

	info := DiscordInfo{Name: l.Name, URL: l.URL}
	u, err := url.Parse(l.URL)
	if err != nil {
		return info
	}
	code := u.Path[strings.LastIndex(u.Path, "/")+1:]
	req, _ := http.NewRequest("GET", "https://discord.com/api/v10/invites/"+url.PathEscape(code)+"?with_counts=true", nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return info
	}
	defer resp.Body.Close()
	var d struct {
		Members int `json:"approximate_member_count"`
		Online  int `json:"approximate_presence_count"`
		Guild   struct {
			ID   string `json:"id"`
			Icon string `json:"icon"`
		} `json:"guild"`
	}
	if resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&d) == nil && d.Members > 0 {
		info.OK, info.Members, info.Online = true, d.Members, d.Online
		if d.Guild.ID != "" && d.Guild.Icon != "" {
			ext := "png"
			if strings.HasPrefix(d.Guild.Icon, "a_") {
				ext = "gif"
			}
			info.Icon = "https://cdn.discordapp.com/icons/" + d.Guild.ID + "/" + d.Guild.Icon + "." + ext + "?size=64"
		}
		discordMu.Lock()
		discordCache[l.URL], discordAt[l.URL] = info, time.Now()
		discordMu.Unlock()
	}
	return info
}
