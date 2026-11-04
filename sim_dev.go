//go:build devsim

// Simulation build for testing self-update against a local server. Never part of a
// release: it trusts loopback http URLs and logs to sim.log next to the exe. With
// IME_SIM_AUTO=1 it also starts the update on its own.
package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var simOnce sync.Once

func simLog(format string, args ...any) {
	exe, err := selfPath()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "sim.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d v=%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000"), os.Getpid(), Version}, args...)...)
}

func init() {
	trustedURL = func(u *url.URL, _ bool) bool { return u.Scheme == "http" && u.Hostname() == "127.0.0.1" }
	simLog("start")
	var lastState string
	onUpdate = func(p UpdateProgress) {
		if p.State != lastState {
			lastState = p.State
			simLog("update %s done=%d total=%d err=%q", p.State, p.Done, p.Total, p.Error)
		}
	}
	onStatus = func(a *App, st LauncherStatus) {
		simLog("status %s blocked=%v selfUpdate=%v latest=%s", st.State, st.Blocked, st.SelfUpdate, st.Latest)
		if st.SelfUpdate && os.Getenv("IME_SIM_AUTO") == "1" {
			simOnce.Do(func() {
				if err := a.StartUpdate(); err != nil {
					simLog("StartUpdate error: %v", err)
				}
			})
		}
	}
}
