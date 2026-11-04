package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// releasesAPI is the GitHub releases endpoint of this repo. It is a var only so the
// simulation build can point it at a local server with -ldflags -X.
var releasesAPI = "https://api.github.com/repos/irhagram/IME-Launcher/releases"

// ReleaseNotes is the text of a GitHub release, shown in the update popup.
type ReleaseNotes struct {
	OK        bool   `json:"ok"`
	Tag       string `json:"tag"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	Published int64  `json:"published"` // unix ms
}

var (
	notesMu    sync.Mutex
	notesCache = map[string]ReleaseNotes{}
)

// GetReleaseNotes returns the notes of the release tagged version (the tag may be
// written with or without a leading "v"); if no such release exists it falls back to
// the latest release. Only successful lookups are cached.
func (a *App) GetReleaseNotes(version string) ReleaseNotes {
	version = strings.TrimSpace(version)
	if version == "" {
		return ReleaseNotes{}
	}
	notesMu.Lock()
	if n, ok := notesCache[version]; ok {
		notesMu.Unlock()
		return n
	}
	notesMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	n, err := fetchNotes(ctx, releasesAPI, version)
	if err != nil {
		return ReleaseNotes{}
	}
	notesMu.Lock()
	notesCache[version] = n
	notesMu.Unlock()
	return n
}

func fetchNotes(ctx context.Context, base, version string) (ReleaseNotes, error) {
	v := strings.TrimLeft(version, "vV")
	var lastErr error
	for _, path := range []string{"/tags/" + url.PathEscape(v), "/tags/" + url.PathEscape("v"+v), "/latest"} {
		n, err := fetchRelease(ctx, base+path)
		if err == nil {
			return n, nil
		}
		lastErr = err
	}
	return ReleaseNotes{}, lastErr
}

func fetchRelease(ctx context.Context, rawURL string) (ReleaseNotes, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return ReleaseNotes{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return ReleaseNotes{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReleaseNotes{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return ReleaseNotes{}, err
	}
	var r struct {
		Tag       string `json:"tag_name"`
		Name      string `json:"name"`
		Body      string `json:"body"`
		URL       string `json:"html_url"`
		Published string `json:"published_at"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return ReleaseNotes{}, err
	}
	if r.Tag == "" {
		return ReleaseNotes{}, errors.New("bukan data release")
	}
	n := ReleaseNotes{OK: true, Tag: r.Tag, Title: r.Name, Body: strings.ReplaceAll(r.Body, "\r\n", "\n")}
	if safe := safeDownloadURL(r.URL); safe != "" {
		n.URL = safe
	}
	if t, err := time.Parse(time.RFC3339, r.Published); err == nil {
		n.Published = t.UnixMilli()
	}
	return n, nil
}
