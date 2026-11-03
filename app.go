package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const (
	userAgent = "IMELauncher/1.0 (+https://imeroleplay.com)"
)

type App struct {
	ctx context.Context
	cfg *configStore
	dl  *downloader
}

func NewApp() *App {
	a := &App{}
	a.cfg = newConfigStore(filepath.Join(a.dataDir(), "config.json"))
	a.dl = newDownloader(a)
	return a
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// dataDir returns %AppData%\IMELauncher (plus optional sub elements).
func (a *App) dataDir(sub ...string) string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(append([]string{base, "IMELauncher"}, sub...)...)
}

func (a *App) emit(name string, data any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data)
	}
}

func (a *App) gate(id string) (Gate, bool) {
	for _, g := range a.cfg.get().Gates {
		if g.ID == id {
			return g, true
		}
	}
	return Gate{}, false
}

// ---- window ----

func (a *App) Minimise() { runtime.WindowMinimise(a.ctx) }
func (a *App) Quit()     { runtime.Quit(a.ctx) }

// SetHeight resizes the window to fit the active tab.
func (a *App) SetHeight(h int) { runtime.WindowSetSize(a.ctx, 1040, h) }

// ---- config ----

func (a *App) GetConfig() Config { return a.cfg.get() }

func (a *App) OpenURL(u string) {
	if strings.HasPrefix(u, "https://") {
		runtime.BrowserOpenURL(a.ctx, u)
	}
}

// ---- player count ----

type PlayerInfo struct {
	OK      bool   `json:"ok"`
	Players int    `json:"players"`
	Max     int    `json:"max"`
	Name    string `json:"name"`
	Error   string `json:"error,omitempty"`
}

// GetPlayers reads the FiveM server's public dynamic.json (served on the game port).
func (a *App) GetPlayers() PlayerInfo {
	var lastErr string
	for _, g := range a.cfg.get().Gates {
		info, err := fetchDynamic(g)
		if err == nil {
			return info
		}
		lastErr = err.Error()
	}
	return PlayerInfo{Error: lastErr}
}

func fetchDynamic(g Gate) (PlayerInfo, error) {
	cl := &http.Client{Timeout: 6 * time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:%d/dynamic.json", g.Host, g.Port), nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := cl.Do(req)
	if err != nil {
		return PlayerInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return PlayerInfo{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var d struct {
		Clients  int    `json:"clients"`
		Max      string `json:"sv_maxclients"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return PlayerInfo{}, err
	}
	var max int
	fmt.Sscanf(d.Max, "%d", &max)
	return PlayerInfo{OK: true, Players: d.Clients, Max: max, Name: d.Hostname}, nil
}

// ---- FiveM discovery & connect ----

func isFiveMData(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "data"))
	return err == nil && st.IsDir()
}

// normaliseFiveMDir accepts the FiveM folder or FiveM.app and returns FiveM.app.
func normaliseFiveMDir(dir string) string {
	if isFiveMData(dir) && strings.EqualFold(filepath.Base(dir), "FiveM.app") {
		return dir
	}
	if sub := filepath.Join(dir, "FiveM.app"); isFiveMData(sub) {
		return sub
	}
	if isFiveMData(dir) {
		return dir
	}
	return ""
}

// LocateFiveM auto-detects (or returns the saved) FiveM Application Data folder.
func (a *App) LocateFiveM() string {
	if d := a.cfg.get().FiveMDir; d != "" && isFiveMData(d) {
		return d
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		if d := normaliseFiveMDir(filepath.Join(la, "FiveM")); d != "" {
			a.cfg.update(func(c *Config) { c.FiveMDir = d })
			return d
		}
	}
	return ""
}

// PickFiveM lets the user browse to the FiveM Application Data folder (FiveM.app).
func (a *App) PickFiveM() (string, error) {
	start := ""
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		start = filepath.Join(la, "FiveM")
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Pilih folder FiveM Application Data (FiveM.app)",
		DefaultDirectory: existingDir(start),
	})
	if err != nil || dir == "" {
		return "", err
	}
	norm := normaliseFiveMDir(dir)
	if norm == "" {
		return "", fmt.Errorf("folder tidak valid: tidak ada folder \"data\" di %s", dir)
	}
	a.cfg.update(func(c *Config) { c.FiveMDir = norm })
	return norm, nil
}

type ConnectResult struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

// Connect hands fivem://connect/<host> to Explorer. FiveM refuses to start when
// its parent is not the shell ("should be launched directly from the shell", or
// "no master process" when the link is sent to a half-started instance), so we
// never spawn FiveM.exe ourselves: Explorer starts it with the link when it is
// closed and delivers the link to the running instance otherwise.
func (a *App) Connect(gateID string) ConnectResult {
	g, ok := a.gate(gateID)
	if !ok {
		return ConnectResult{Msg: "gate tidak dikenal"}
	}

	// the server needs its own FiveM settings in place before the game starts
	fivem := a.LocateFiveM()
	if fivem == "" {
		return ConnectResult{Msg: "Pilih lokasi FiveM dulu lewat tombol Lokasi FiveM di atas."}
	}
	changed, err := applyFiveMSettings(fivem, a.cfg.get().FiveMSettings)
	if err != nil {
		return ConnectResult{Msg: "Gagal menerapkan konfigurasi FiveM: " + err.Error()}
	}
	if changed && fivemRunning() {
		return ConnectResult{Msg: "Konfigurasi IME baru diterapkan. Tutup FiveM sepenuhnya, lalu klik Connect lagi."}
	}

	cmd := exec.Command("explorer.exe", "fivem://connect/"+g.Host)
	if err := cmd.Start(); err != nil {
		return ConnectResult{Msg: "gagal membuka FiveM: " + err.Error()}
	}
	_ = cmd.Process.Release()
	return ConnectResult{OK: true, Msg: "Membuka FiveM & connecting ke " + g.Name + "..."}
}

func (a *App) OpenFolder(p string) {
	if p == "" {
		return
	}
	_ = windows.ShellExecute(0, nil, windows.StringToUTF16Ptr(p), nil, nil, windows.SW_SHOWNORMAL)
}
