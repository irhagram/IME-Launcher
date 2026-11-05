package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keepApp(t *testing.T) (*App, string) {
	t.Helper()
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)
	t.Setenv("LOCALAPPDATA", t.TempDir()) // no FiveM folder: only fivem.cfg is touched
	cfgPath, _ := fivemCfgPath()
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	os.WriteFile(cfgPath, []byte("unbindall\r\nseta \"cl_nickname\" \"Lama\"\r\n"), 0o644)
	a := &App{cfg: newConfigStore(filepath.Join(appdata, "config.json"))}
	a.status = LauncherStatus{State: "ok"}
	return a, cfgPath
}

func TestKeepAndRemoveNickname(t *testing.T) {
	a, _ := keepApp(t)
	if r := a.KeepNickname("  Budi  "); !r.OK {
		t.Fatal(r.Msg)
	}
	if got := a.GetNickname().Kept; got != "Budi" {
		t.Errorf("kept = %q", got)
	}
	// persisted in config.json
	if b := newConfigStore(filepath.Join(filepath.Dir(a.cfg.path), "config.json")).get().KeptNick; b != "Budi" {
		t.Errorf("not persisted: %q", b)
	}
	if r := a.KeepNickname(`bad"name`); r.OK {
		t.Error("invalid name must be refused")
	}
	if a.GetNickname().Kept != "Budi" {
		t.Error("a refused keep must not change the kept name")
	}
	if r := a.RemoveKeptNickname(); !r.OK || a.GetNickname().Kept != "" {
		t.Errorf("remove failed: %+v", r)
	}
}

func TestApplyKeptNickname(t *testing.T) {
	a, cfgPath := keepApp(t)
	old := fivemRunning
	fivemRunning = func() bool { return false }
	defer func() { fivemRunning = old }()

	if note := a.applyKeptNickname(); note != "" {
		t.Errorf("nothing kept: %q", note)
	}
	a.KeepNickname("Budi")
	if note := a.applyKeptNickname(); !strings.Contains(note, "Budi") {
		t.Errorf("note = %q", note)
	}
	if b, _ := os.ReadFile(cfgPath); !strings.Contains(string(b), `seta "cl_nickname" "Budi"`) {
		t.Errorf("cfg = %q", b)
	}
	// already in place: silent and no rewrite
	if note := a.applyKeptNickname(); note != "" {
		t.Errorf("already applied should be silent: %q", note)
	}
	// the name changed afterwards: the next Connect puts the kept one back
	os.WriteFile(cfgPath, []byte("seta \"cl_nickname\" \"Orang Lain\"\r\n"), 0o644)
	a.applyKeptNickname()
	if b, _ := os.ReadFile(cfgPath); !strings.Contains(string(b), `"Budi"`) {
		t.Errorf("kept name not restored: %q", b)
	}
	// FiveM open: nothing is written, Connect is told why
	os.WriteFile(cfgPath, []byte("seta \"cl_nickname\" \"Orang Lain\"\r\n"), 0o644)
	fivemRunning = func() bool { return true }
	if note := a.applyKeptNickname(); !strings.Contains(note, "FiveM sedang terbuka") {
		t.Errorf("note = %q", note)
	}
	if b, _ := os.ReadFile(cfgPath); !strings.Contains(string(b), "Orang Lain") {
		t.Error("cfg must not change while FiveM is open")
	}
}
