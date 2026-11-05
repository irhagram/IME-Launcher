package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// reRbindLine matches a resource key mapping in fivem.cfg: rbind <resource> <mapper> <key> "<command>".
// FiveM writes these for every RegisterKeyMapping, with the key the player picked.
var reRbindLine = regexp.MustCompile(`(?i)^\s*rbind\s+(\S+)\s+(\S+)\s+(\S+)\s*(.*?)\s*$`)

// IMEBind is one rbind line of a server resource.
type IMEBind struct {
	Resource string `json:"resource"`
	Mapper   string `json:"mapper"`
	Key      string `json:"key"`
	Command  string `json:"command"`
}

// IMEKeybindList is what the "IME Keybinds" table shows.
type IMEKeybindList struct {
	OK      bool      `json:"ok"`
	Items   []IMEBind `json:"items"`
	Running bool      `json:"running"`
	Error   string    `json:"error,omitempty"`
}

var (
	resMu    sync.Mutex
	resSet   map[string]bool
	resAt    time.Time
	resTTL   = 10 * time.Minute
	resFetch = func(id string) ([]string, error) { // var so tests can stub the network
		d, err := fetchServerDoc(id)
		return d.Resources, err
	}
)

// serverResources returns the lower-cased names of the server's resources from 5metrics.
func (a *App) serverResources() (map[string]bool, error) {
	resMu.Lock()
	defer resMu.Unlock()
	if resSet != nil && time.Since(resAt) < resTTL {
		return resSet, nil
	}
	list, err := resFetch(a.cfg.get().MetricsID)
	if err != nil {
		if resSet != nil { // keep the last good list
			return resSet, nil
		}
		return nil, fmt.Errorf("gagal mengambil daftar resource dari 5metrics: %w", err)
	}
	if len(list) == 0 {
		return nil, errors.New("daftar resource dari 5metrics kosong")
	}
	set := make(map[string]bool, len(list))
	for _, r := range list {
		set[strings.ToLower(strings.TrimSpace(r))] = true
	}
	resSet, resAt = set, time.Now()
	return set, nil
}

func parseRbindLine(l string) (IMEBind, bool) {
	m := reRbindLine.FindStringSubmatch(l)
	if m == nil || m[4] == "" {
		return IMEBind{}, false
	}
	cmd := m[4]
	if len(cmd) >= 2 && cmd[0] == '"' && cmd[len(cmd)-1] == '"' {
		cmd = cmd[1 : len(cmd)-1]
	}
	return IMEBind{Resource: m[1], Mapper: strings.ToUpper(m[2]), Key: strings.ToUpper(m[3]), Command: cmd}, true
}

// parseIMEBinds returns the rbind lines whose resource is in set, in file order.
func parseIMEBinds(text string, set map[string]bool) []IMEBind {
	lines, _ := splitCfg(text)
	out := []IMEBind{}
	for _, l := range lines {
		if b, ok := parseRbindLine(l); ok && set[strings.ToLower(b.Resource)] {
			out = append(out, b)
		}
	}
	return out
}

// applyIMEBinds rewrites the mapper and key of the server-resource rbind lines, in file
// order. Resource and command must match what is in the file, so a stale list cannot
// change the wrong line. Lines that did not change keep their exact text.
func applyIMEBinds(text string, set map[string]bool, edits []IMEBind) (string, error) {
	lines, eol := splitCfg(text)
	n, changed := 0, 0
	for i, l := range lines {
		b, ok := parseRbindLine(l)
		if !ok || !set[strings.ToLower(b.Resource)] {
			continue
		}
		if n >= len(edits) {
			return "", errors.New("daftar keybind berubah, muat ulang dulu")
		}
		e := edits[n]
		n++
		if e.Resource != b.Resource || e.Command != b.Command {
			return "", errors.New("daftar keybind berubah, muat ulang dulu")
		}
		e.Mapper, e.Key = strings.ToUpper(strings.TrimSpace(e.Mapper)), strings.ToUpper(strings.TrimSpace(e.Key))
		if e.Mapper == "" {
			e.Mapper = "KEYBOARD"
		}
		if !reBindToken.MatchString(e.Mapper) {
			return "", fmt.Errorf("jenis input %q tidak valid", e.Mapper)
		}
		if !reBindToken.MatchString(e.Key) {
			return "", fmt.Errorf("tombol %q tidak valid (huruf, angka, dan _ saja, contoh: I, F5, NUMPAD0)", e.Key)
		}
		if e.Mapper != b.Mapper || e.Key != b.Key {
			changed++
			lines[i] = `rbind ` + b.Resource + ` ` + e.Mapper + ` ` + e.Key + ` "` + b.Command + `"`
		}
	}
	if n != len(edits) {
		return "", errors.New("daftar keybind berubah, muat ulang dulu")
	}
	if changed == 0 {
		return text, nil
	}
	return strings.Join(lines, eol) + eol, nil
}

// GetIMEKeybinds lists the rbind lines of fivem.cfg that belong to the server's resources.
func (a *App) GetIMEKeybinds() IMEKeybindList {
	res := IMEKeybindList{Items: []IMEBind{}, Running: fivemRunning()}
	set, err := a.serverResources()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	path, err := fivemCfgPath()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			res.OK, res.Error = false, err.Error()
		}
		return res
	}
	res.Items = parseIMEBinds(string(b), set)
	return res
}

// SaveIMEKeybinds saves the changed keys of the server-resource binds to fivem.cfg.
func (a *App) SaveIMEKeybinds(items []IMEBind) SettingsResult {
	if !a.allowed() {
		return SettingsResult{Msg: a.lockedMsg()}
	}
	if fivemRunning() {
		return SettingsResult{Msg: "FiveM sedang terbuka. Tutup FiveM sepenuhnya dulu, karena FiveM menulis ulang fivem.cfg saat ditutup."}
	}
	set, err := a.serverResources()
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	path, err := fivemCfgPath()
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		return SettingsResult{Msg: "Gagal membaca fivem.cfg: " + err.Error()}
	}
	next, err := applyIMEBinds(string(orig), set, items)
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	if next == string(orig) {
		return SettingsResult{OK: true, Msg: "Tidak ada perubahan."}
	}
	if err := commitCfg(path, orig, true, next); err != nil {
		return SettingsResult{Msg: "Gagal menulis fivem.cfg: " + err.Error()}
	}
	return SettingsResult{OK: true, Msg: "Keybind tersimpan."}
}
