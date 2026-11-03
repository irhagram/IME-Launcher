package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const (
	binName       = "ImeCache-1.bin"
	exeName       = "ImeCache.exe"
	cdnBase       = "https://cdn.ferrenza.com/download/"
	maxRetries    = 8
	progressEvery = 500 * time.Millisecond
)

var (
	reBin = regexp.MustCompile(`download/\?file=bin&(?:amp;)?v=(\d+)`)
	reExe = regexp.MustCompile(`download/\?file=exe&(?:amp;)?v=(\d+)`)
)

type CacheInfo struct {
	OK         bool   `json:"ok"`
	BinVersion string `json:"binVersion"`
	ExeVersion string `json:"exeVersion"`
	BinURL     string `json:"binUrl"`
	ExeURL     string `json:"exeUrl"`
	Size       int64  `json:"size"`
	Updated    int64  `json:"updated"` // Last-Modified of the .bin on the CDN, unix ms
	Dir        string `json:"dir"`
	FiveM      string `json:"fivem"`
	Target     string `json:"target"`
	Error      string `json:"error,omitempty"`
}

type DLProgress struct {
	State      string  `json:"state"` // idle | downloading | paused | done | error
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Percent    float64 `json:"percent"`
	Speed      float64 `json:"speed"` // bytes/s
	ETA        int64   `json:"eta"`   // seconds, -1 unknown
	Path       string  `json:"path"`
	Message    string  `json:"message,omitempty"`
}

type downloader struct {
	app    *App
	mu     sync.Mutex
	cancel context.CancelFunc
	last   DLProgress
}

func newDownloader(a *App) *downloader {
	return &downloader{app: a, last: DLProgress{State: "idle", ETA: -1}}
}

func (d *downloader) publish(p DLProgress) {
	d.mu.Lock()
	d.last = p
	d.mu.Unlock()
	d.app.emit("cache:progress", p)
}

func (a *App) defaultDownloadDir() string {
	if d := a.cfg.get().DownloadDir; d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads", "IMECache")
}

func (a *App) fetchCacheInfo() CacheInfo {
	info := CacheInfo{Dir: a.defaultDownloadDir(), FiveM: a.LocateFiveM()}
	if info.FiveM != "" {
		info.Target = filepath.Join(info.FiveM, "data", "server-cache-priv")
	}
	cl := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", a.cfg.get().CacheURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := cl.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	mb, me := reBin.FindSubmatch(body), reExe.FindSubmatch(body)
	if mb == nil {
		info.Error = "versi cache tidak ditemukan di halaman CDN"
		return info
	}
	info.BinVersion = string(mb[1])
	info.BinURL = cdnBase + "?file=bin&v=" + info.BinVersion
	if me != nil {
		info.ExeVersion = string(me[1])
		info.ExeURL = cdnBase + "?file=exe&v=" + info.ExeVersion
	}
	head, err := http.NewRequest("HEAD", info.BinURL, nil)
	if err == nil {
		head.Header.Set("User-Agent", userAgent)
		if r, err := cl.Do(head); err == nil {
			info.Size = r.ContentLength
			if t, err := http.ParseTime(r.Header.Get("Last-Modified")); err == nil {
				info.Updated = t.UnixMilli()
			}
			r.Body.Close()
		}
	}
	info.OK = true
	return info
}

// GetCacheInfo returns the latest cache version, size, and local paths.
func (a *App) GetCacheInfo() CacheInfo { return a.fetchCacheInfo() }

// GetDownloadState lets the UI re-sync after a reload.
func (a *App) GetDownloadState() DLProgress {
	a.dl.mu.Lock()
	defer a.dl.mu.Unlock()
	return a.dl.last
}

// PickDownloadDir lets the user choose where the 30 GB file is stored.
func (a *App) PickDownloadDir() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Pilih folder penyimpanan cache",
		DefaultDirectory: existingDir(a.defaultDownloadDir()),
	})
	if err != nil || dir == "" {
		return "", err
	}
	a.cfg.update(func(c *Config) { c.DownloadDir = dir })
	return dir, nil
}

// existingDir walks up until it finds a folder that exists (the dialog fails on a missing default).
func existingDir(p string) string {
	for p != "" {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return ""
}

func freeSpace(dir string) (int64, error) {
	var avail uint64
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return int64(avail), nil
}

// StartDownload downloads (or resumes) ImeCache-1.bin.
func (a *App) StartDownload() error {
	d := a.dl
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		return errors.New("download sedang berjalan")
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.mu.Unlock()

	go func() {
		err := a.runDownload(ctx)
		d.mu.Lock()
		d.cancel = nil
		last := d.last
		d.mu.Unlock()
		switch {
		case errors.Is(err, context.Canceled):
			last.State, last.Speed, last.ETA, last.Message = "paused", 0, -1, "Dijeda"
			d.publish(last)
		case err != nil:
			last.State, last.Speed, last.ETA, last.Message = "error", 0, -1, err.Error()
			d.publish(last)
		}
	}()
	return nil
}

// PauseDownload stops the transfer but keeps the partial file for resuming.
func (a *App) PauseDownload() {
	a.dl.mu.Lock()
	defer a.dl.mu.Unlock()
	if a.dl.cancel != nil {
		a.dl.cancel()
	}
}

func (a *App) runDownload(ctx context.Context) error {
	info := a.fetchCacheInfo()
	if !info.OK {
		return fmt.Errorf("gagal cek versi cache: %s", info.Error)
	}
	if info.Size <= 0 {
		return errors.New("ukuran file tidak diketahui dari CDN")
	}
	dir := a.defaultDownloadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, binName)
	verFile := path + ".ver"

	// A different version than the partial file means starting over.
	if b, err := os.ReadFile(verFile); err != nil || strings.TrimSpace(string(b)) != info.BinVersion {
		_ = os.Remove(path)
		_ = os.WriteFile(verFile, []byte(info.BinVersion), 0o644)
	}
	var have int64
	if st, err := os.Stat(path); err == nil {
		have = st.Size()
		if have > info.Size {
			_ = os.Remove(path)
			have = 0
		}
	}
	if free, err := freeSpace(dir); err == nil && free < info.Size-have {
		return fmt.Errorf("ruang disk kurang: butuh %.1f GB, tersedia %.1f GB",
			float64(info.Size-have)/(1<<30), float64(free)/(1<<30))
	}

	prog := DLProgress{State: "downloading", Downloaded: have, Total: info.Size, ETA: -1, Path: path}
	if have == info.Size {
		prog.State, prog.Percent, prog.ETA = "done", 100, 0
		a.dl.publish(prog)
		return nil
	}
	a.dl.publish(prog)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			prog.Message = fmt.Sprintf("Koneksi terputus, mencoba lagi (%d/%d)...", attempt, maxRetries)
			a.dl.publish(prog)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		n, err := a.transfer(ctx, info, path, &prog)
		if err == nil {
			_ = n
			prog.State, prog.Percent, prog.ETA, prog.Speed, prog.Message = "done", 100, 0, 0, "Download selesai"
			a.dl.publish(prog)
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// transfer performs one ranged GET from the current end of the file.
func (a *App) transfer(ctx context.Context, info CacheInfo, path string, prog *DLProgress) (int64, error) {
	var have int64
	if st, err := os.Stat(path); err == nil {
		have = st.Size()
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", info.BinURL, nil)
	req.Header.Set("User-Agent", userAgent)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	cl := &http.Client{} // no overall timeout: file is huge
	resp, err := cl.Do(req)
	if err != nil {
		return have, err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK:
		have = 0 // server ignored Range: restart
		flags |= os.O_TRUNC
	default:
		return have, fmt.Errorf("CDN membalas HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return have, err
	}
	defer f.Close()
	if _, err := f.Seek(have, io.SeekStart); err != nil {
		return have, err
	}

	prog.State, prog.Message = "downloading", ""
	buf := make([]byte, 1<<20)
	done := have
	lastT, lastB := time.Now(), done
	var speed float64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
		}
		if now := time.Now(); now.Sub(lastT) >= progressEvery {
			inst := float64(done-lastB) / now.Sub(lastT).Seconds()
			if speed == 0 {
				speed = inst
			} else {
				speed = speed*0.7 + inst*0.3
			}
			lastT, lastB = now, done
			prog.Downloaded, prog.Speed = done, speed
			prog.Percent = float64(done) / float64(info.Size) * 100
			prog.ETA = -1
			if speed > 1 {
				prog.ETA = int64(float64(info.Size-done) / speed)
			}
			a.dl.publish(*prog)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return done, rerr
		}
	}
	prog.Downloaded = done
	if done != info.Size {
		return done, fmt.Errorf("ukuran tidak lengkap (%d dari %d byte)", done, info.Size)
	}
	return done, nil
}

// ---- installer / extract ----

type ExtractResult struct {
	OK     bool   `json:"ok"`
	Msg    string `json:"msg"`
	Target string `json:"target"`
}

// ExtractCache makes sure ImeCache.exe sits next to the downloaded .bin and
// launches it; the official installer auto-detects the .bin and the FiveM folder
// and unpacks into FiveM.app\data\server-cache-priv.
func (a *App) ExtractCache() ExtractResult {
	dir := a.defaultDownloadDir()
	bin := filepath.Join(dir, binName)
	st, err := os.Stat(bin)
	if err != nil {
		return ExtractResult{Msg: "File cache belum diunduh"}
	}
	info := a.fetchCacheInfo()
	if info.Size > 0 && st.Size() != info.Size {
		return ExtractResult{Msg: "Download cache belum selesai"}
	}
	fivem := a.LocateFiveM()
	if fivem == "" {
		return ExtractResult{Msg: "Lokasi FiveM Application Data belum dipilih"}
	}
	target := filepath.Join(fivem, "data", "server-cache-priv")
	if info.ExeURL == "" {
		return ExtractResult{Msg: "Link ImeCache.exe tidak ditemukan di CDN"}
	}

	exe := filepath.Join(dir, exeName)
	if err := a.fetchSmall(info.ExeURL, exe); err != nil {
		return ExtractResult{Msg: "Gagal mengunduh " + exeName + ": " + err.Error()}
	}
	err = windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(exe),
		nil, windows.StringToUTF16Ptr(dir), windows.SW_SHOWNORMAL)
	if err != nil {
		return ExtractResult{Msg: "Gagal menjalankan installer: " + err.Error()}
	}
	return ExtractResult{OK: true, Target: target, Msg: "Installer dijalankan. Ikuti langkahnya sampai selesai."}
}

func (a *App) fetchSmall(u, dest string) error {
	cl := &http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("HTTP " + strconv.Itoa(resp.StatusCode))
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, dest)
}
