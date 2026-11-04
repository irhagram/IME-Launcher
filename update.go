package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const maxUpdateSize = 150 << 20

var reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// trustedURL decides which URLs the updater may talk to. Release downloads start at
// github.com and are redirected to GitHub's asset hosts, so those are the only places
// an update can come from. The devsim build tag widens it to loopback for tests.
var trustedURL = githubTrusted

func githubTrusted(u *url.URL, redirect bool) bool {
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "github.com" || h == "www.github.com" {
		return true
	}
	return redirect && strings.HasSuffix(h, ".githubusercontent.com")
}

// UpdateProgress is streamed to the UI as "update:progress".
type UpdateProgress struct {
	State string  `json:"state"` // downloading | verifying | applying | restarting | error
	Done  int64   `json:"done"`
	Total int64   `json:"total"`
	Speed float64 `json:"speed"` // bytes per second
	Error string  `json:"error,omitempty"`
}

// Dev hooks (nil in release builds, set by sim_dev.go).
var (
	onStatus func(a *App, st LauncherStatus)
	onUpdate func(p UpdateProgress)
)

// StartUpdate downloads the new exe in the background, checks it against the SHA-256
// published in launcher.json, swaps it in for the running exe and restarts. Progress
// and errors arrive as "update:progress" events; on error the old exe stays in place.
func (a *App) StartUpdate() error {
	st := a.GetStatus()
	if !st.SelfUpdate {
		return errors.New("update otomatis tidak tersedia, gunakan download manual")
	}
	a.updMu.Lock()
	if a.updating {
		a.updMu.Unlock()
		return errors.New("update sedang berjalan")
	}
	a.updating = true
	a.updMu.Unlock()

	go func() {
		err := a.runUpdate(st)
		a.updMu.Lock()
		a.updating = false
		a.updMu.Unlock()
		if err != nil {
			a.emitUpdate(UpdateProgress{State: "error", Error: err.Error()})
		}
	}()
	return nil
}

func (a *App) emitUpdate(p UpdateProgress) {
	if onUpdate != nil {
		onUpdate(p)
	}
	a.emit("update:progress", p)
}

func (a *App) runUpdate(st LauncherStatus) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	newPath, oldPath := exe+".new", exe+".old"

	err = fetchFile(context.Background(), st.DownloadURL, newPath, st.SHA256, func(p UpdateProgress) { a.emitUpdate(p) })
	if err != nil {
		os.Remove(newPath)
		return err
	}

	a.emitUpdate(UpdateProgress{State: "applying"})
	if err := swapExe(exe, newPath, oldPath); err != nil {
		os.Remove(newPath)
		return err
	}

	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		// The new exe won't start: put the old one back so the launcher keeps working.
		os.Remove(exe)
		os.Rename(oldPath, exe)
		return fmt.Errorf("launcher baru gagal dijalankan: %w", err)
	}
	a.emitUpdate(UpdateProgress{State: "restarting"})
	time.Sleep(600 * time.Millisecond)
	runtime.Quit(a.ctx)
	return nil
}

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// fetchFile downloads rawURL to dst and keeps it only if its SHA-256 equals wantSHA.
func fetchFile(ctx context.Context, rawURL, dst, wantSHA string, report func(UpdateProgress)) error {
	u, err := url.Parse(rawURL)
	if err != nil || !trustedURL(u, false) {
		return errors.New("alamat download tidak dipercaya")
	}
	client := &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("terlalu banyak redirect")
			}
			if !trustedURL(req.URL, true) {
				return errors.New("redirect ke alamat yang tidak dipercaya")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gagal mengunduh update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gagal mengunduh update: HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total > maxUpdateSize {
		return errors.New("file update terlalu besar")
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("folder launcher tidak bisa ditulis: %w", err)
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var done int64
	start, last := time.Now(), time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxUpdateSize {
				f.Close()
				return errors.New("file update terlalu besar")
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			if time.Since(last) > 150*time.Millisecond {
				last = time.Now()
				report(UpdateProgress{State: "downloading", Done: done, Total: total, Speed: float64(done) / time.Since(start).Seconds()})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("unduhan terputus: %w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	report(UpdateProgress{State: "downloading", Done: done, Total: done})
	report(UpdateProgress{State: "verifying"})
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return errors.New("checksum file update tidak cocok, update dibatalkan")
	}
	return nil
}

// swapExe renames the running exe out of the way (Windows allows renaming, not
// overwriting, a running exe) and moves the new one into its place. On failure the
// original is restored.
func swapExe(exe, newPath, oldPath string) error {
	os.Remove(oldPath) // leftover from an earlier update; fails harmlessly if still locked
	if err := os.Rename(exe, oldPath); err != nil {
		return fmt.Errorf("tidak bisa mengganti launcher (folder butuh izin?): %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(oldPath, exe)
		return fmt.Errorf("tidak bisa memasang launcher baru: %w", err)
	}
	return nil
}

// cleanupUpdate removes files an update leaves behind. The .old exe stays locked until
// the previous process has exited, so it is retried for a while.
func cleanupUpdate() {
	exe, err := selfPath()
	if err != nil {
		return
	}
	os.Remove(exe + ".new")
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(exe + ".old"); err != nil || os.Remove(exe+".old") == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

func validSHA256(s string) bool { return reSHA256.MatchString(s) }
