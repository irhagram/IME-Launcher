package main

import (
	"bytes"
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
)

// Version is this build's version. Override at build time with
// -ldflags "-X main.Version=1.0.5" (keep winres/winres.json in step).
var Version = "1.0.5"

// statusURL is deliberately not part of config.json, so it cannot be repointed by
// editing a local file. It is a var only so the simulation build can override it
// with -ldflags -X.
var statusURL = "https://raw.githubusercontent.com/irhagram/IME-Launcher/main/launcher.json"

const (
	statusRefresh = 10 * time.Minute
	statusRetry   = 20 * time.Second
)

// LauncherStatus is what the UI shows. Blocked statuses lock every feature.
type LauncherStatus struct {
	State       string `json:"state"` // checking | ok | update | force | maintenance | disabled | offline
	Blocked     bool   `json:"blocked"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Minimum     string `json:"minimum"`
	DownloadURL string `json:"downloadUrl"`
	SelfUpdate  bool   `json:"selfUpdate"` // the launcher can update itself (download_url + sha256 are usable)
	SHA256      string `json:"-"`
}

type remoteDoc struct {
	Launcher *struct {
		Enabled        *bool  `json:"enabled"`
		MinimumVersion string `json:"minimum_version"`
		LatestVersion  string `json:"latest_version"`
		ForceUpdate    bool   `json:"force_update"`
		Maintenance    bool   `json:"maintenance"`
		DownloadURL    string `json:"download_url"`
		SHA256         string `json:"sha256"`
		Message        string `json:"message"`
	} `json:"launcher"`
}

// compareVersions compares dotted numeric versions ("1.0.2", "v1.10"); extra
// suffixes like "-beta" are ignored. It returns -1, 0 or 1.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(v), "vV"))
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// safeDownloadURL only lets the UI open https links on github.com, so a tampered
// JSON cannot send players to an arbitrary site.
func safeDownloadURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !trustedURL(u, false) {
		return ""
	}
	return u.String()
}

// decide turns the remote document into a status for this build version.
func decide(cur string, doc remoteDoc) LauncherStatus {
	l := doc.Launcher
	st := LauncherStatus{
		State: "ok", Current: cur,
		Latest: l.LatestVersion, Minimum: l.MinimumVersion,
		DownloadURL: safeDownloadURL(l.DownloadURL),
		Message:     strings.TrimSpace(l.Message),
	}
	def := func(title, msg string) {
		st.Title = title
		if st.Message == "" {
			st.Message = msg
		}
	}
	belowMin := l.MinimumVersion != "" && compareVersions(cur, l.MinimumVersion) < 0
	belowLatest := l.LatestVersion != "" && compareVersions(cur, l.LatestVersion) < 0

	switch {
	case l.Enabled != nil && !*l.Enabled:
		st.State, st.Blocked = "disabled", true
		def("Launcher Dinonaktifkan", "Launcher sedang dinonaktifkan. Silakan coba lagi nanti.")
	case l.Maintenance:
		st.State, st.Blocked = "maintenance", true
		def("Sedang Maintenance", "Server sedang dalam perbaikan. Silakan kembali beberapa saat lagi.")
	case belowMin || (l.ForceUpdate && belowLatest):
		st.State, st.Blocked = "force", true
		def("Update Wajib", "Versi launcher kamu sudah tidak didukung. Download versi terbaru untuk melanjutkan.")
	case belowLatest:
		st.State = "update"
		def("Update Tersedia", "Versi baru launcher sudah tersedia.")
	}
	if sum := strings.ToLower(strings.TrimSpace(l.SHA256)); validSHA256(sum) && st.DownloadURL != "" && (st.State == "update" || st.State == "force") {
		st.SelfUpdate, st.SHA256 = true, sum
	}
	return st
}

func fetchRemote(ctx context.Context, rawURL string) (remoteDoc, error) {
	var doc remoteDoc
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL+sep+"t="+strconv.FormatInt(time.Now().Unix(), 10), nil)
	if err != nil {
		return doc, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return doc, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return doc, err
	}
	// Notepad and some editors save a UTF-8 BOM, which encoding/json rejects.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, err
	}
	if doc.Launcher == nil {
		return doc, errors.New(`objek "launcher" tidak ada`)
	}
	return doc, nil
}

func offlineStatus() LauncherStatus {
	return LauncherStatus{
		State: "offline", Blocked: true, Current: Version,
		Title:   "Tidak Bisa Memeriksa Pembaruan",
		Message: "Launcher perlu terhubung ke internet untuk memeriksa versi. Periksa koneksi kamu lalu coba lagi.",
	}
}

// CheckStatus fetches the remote JSON and applies it. If the JSON cannot be
// fetched the launcher stays locked (fail closed), so blocking the host does not bypass it.
func (a *App) CheckStatus() LauncherStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := offlineStatus()
	if doc, err := fetchRemote(ctx, statusURL); err == nil {
		st = decide(Version, doc)
	}
	a.setStatus(st)
	return st
}

// GetStatus returns the last known status (used by the UI on load).
func (a *App) GetStatus() LauncherStatus {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	return a.status
}

func (a *App) setStatus(st LauncherStatus) {
	a.statusMu.Lock()
	a.status = st
	a.statusMu.Unlock()
	if st.Blocked {
		a.PauseDownload() // a locked launcher must not keep downloading
	}
	a.emit("launcher:status", st)
	if onStatus != nil {
		onStatus(a, st)
	}
}

// allowed reports whether features may run. Every feature method checks this in the
// backend, so hiding the overlay in the UI is not enough to use a locked launcher.
func (a *App) allowed() bool {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	return !a.status.Blocked
}

func (a *App) statusLoop(ctx context.Context) {
	for {
		st := a.CheckStatus()
		wait := statusRefresh
		if st.State == "offline" {
			wait = statusRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
