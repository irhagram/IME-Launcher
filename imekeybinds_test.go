package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rbindCfg = "// generated\r\nunbindall\r\nbind KEYBOARD I \"e tablet\"\r\n" +
	"rbind core KEYBOARD TAB \"+scoreboard\"\r\n" +
	"rbind ime_acs KEYBOARD END \"ime_acs_cancel_sequence\"\r\n" +
	"rbind Ox_Inventory KEYBOARD TAB \"+hotbar\"\r\n" +
	"rbind ime_racing KEYBOARD LEFT \"+move left\"\r\n"

var imeSet = map[string]bool{"ime_acs": true, "ox_inventory": true, "ime_racing": true}

func TestParseIMEBinds(t *testing.T) {
	got := parseIMEBinds(rbindCfg, imeSet)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0] != (IMEBind{"ime_acs", "KEYBOARD", "END", "ime_acs_cancel_sequence"}) || got[1].Resource != "Ox_Inventory" || got[2].Command != "+move left" {
		t.Errorf("got %+v", got)
	}
	for _, b := range got {
		if b.Resource == "core" {
			t.Error("a resource that is not on the server list must be skipped")
		}
	}
}

func TestApplyIMEBinds(t *testing.T) {
	edits := parseIMEBinds(rbindCfg, imeSet)
	edits[1].Key, edits[1].Mapper = "f6", "keyboard"
	got, err := applyIMEBinds(rbindCfg, imeSet, edits)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(rbindCfg, `rbind Ox_Inventory KEYBOARD TAB "+hotbar"`, `rbind Ox_Inventory KEYBOARD F6 "+hotbar"`, 1)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// nothing changed: the text comes back byte for byte
	if same, err := applyIMEBinds(rbindCfg, imeSet, parseIMEBinds(rbindCfg, imeSet)); err != nil || same != rbindCfg {
		t.Errorf("unchanged edit must not touch the file: %v", err)
	}
}

func TestApplyIMEBindsRefusals(t *testing.T) {
	good := parseIMEBinds(rbindCfg, imeSet)
	cases := map[string][]IMEBind{
		"stale": {good[0], good[1]},
		"extra": append(append([]IMEBind{}, good...), IMEBind{"x", "KEYBOARD", "A", "y"}),
	}
	wrongCmd := append([]IMEBind{}, good...)
	wrongCmd[0].Command = "other"
	cases["command differs"] = wrongCmd
	badKey := append([]IMEBind{}, good...)
	badKey[0].Key = "F 5"
	cases["bad key"] = badKey
	badMapper := append([]IMEBind{}, good...)
	badMapper[0].Mapper = "KEY BOARD"
	cases["bad mapper"] = badMapper
	for name, e := range cases {
		if _, err := applyIMEBinds(rbindCfg, imeSet, e); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestSaveIMEKeybinds(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)
	cfgPath, _ := fivemCfgPath()
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	os.WriteFile(cfgPath, []byte(rbindCfg), 0o644)
	oldRun, oldFetch := fivemRunning, resFetch
	fivemRunning = func() bool { return false }
	resFetch = func(string) ([]string, error) { return []string{"ime_acs", "ox_inventory", "ime_racing"}, nil }
	resSet = nil
	defer func() { fivemRunning, resFetch, resSet = oldRun, oldFetch, nil }()

	a := &App{cfg: newConfigStore(filepath.Join(appdata, "config.json"))}
	a.status = LauncherStatus{State: "ok"}

	list := a.GetIMEKeybinds()
	if !list.OK || len(list.Items) != 3 {
		t.Fatalf("%+v", list)
	}
	list.Items[0].Key = "NUMPAD2"
	if r := a.SaveIMEKeybinds(list.Items); !r.OK {
		t.Fatal(r.Msg)
	}
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), `rbind ime_acs KEYBOARD NUMPAD2 "ime_acs_cancel_sequence"`) || !strings.Contains(string(b), "rbind core KEYBOARD TAB") {
		t.Errorf("%q", b)
	}
	if _, err := os.Stat(cfgPath + ".imebak"); err != nil {
		t.Error("backup missing")
	}

	fivemRunning = func() bool { return true }
	if r := a.SaveIMEKeybinds(list.Items); r.OK {
		t.Error("must refuse while FiveM is open")
	}
}

func TestServerResourcesError(t *testing.T) {
	old := resFetch
	resFetch = func(string) ([]string, error) { return nil, os.ErrDeadlineExceeded }
	resSet = nil
	defer func() { resFetch, resSet = old, nil }()
	a := &App{cfg: newConfigStore(filepath.Join(t.TempDir(), "c.json"))}
	if l := a.GetIMEKeybinds(); l.OK || l.Error == "" || len(l.Items) != 0 {
		t.Errorf("no list without resources: %+v", l)
	}
}
