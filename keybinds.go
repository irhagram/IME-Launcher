package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxKeybinds   = 200
	maxBindCmdLen = 200
)

// reBindLine matches a user key bind in fivem.cfg: bind <mapper> <key> <command>.
// "rbind" lines are written by FiveM for resource key mappings and are never matched.
var reBindLine = regexp.MustCompile(`(?i)^\s*bind\s+(\S+)\s+(\S+)\s*(.*?)\s*$`)

// reBindToken limits mapper and key names to what FiveM uses (KEYBOARD, MOUSE_BUTTON, F5, NUMPAD0...).
var reBindToken = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// Keybind is one `bind` line of fivem.cfg.
type Keybind struct {
	Mapper  string `json:"mapper"`
	Key     string `json:"key"`
	Command string `json:"command"`
	// Disabled binds are kept only in the launcher's config, not in fivem.cfg.
	Disabled bool `json:"disabled"`
}

// KeybindList is what the settings tab shows.
type KeybindList struct {
	OK      bool      `json:"ok"`
	Items   []Keybind `json:"items"`
	Path    string    `json:"path"`
	Exists  bool      `json:"exists"`
	Running bool      `json:"running"`
	Error   string    `json:"error,omitempty"`
}

func splitCfg(text string) (lines []string, eol string) {
	eol = "\r\n"
	if !strings.Contains(text, "\r\n") && strings.Contains(text, "\n") {
		eol = "\n"
	}
	if text != "" {
		lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	return lines, eol
}

// parseBindLine reads one cfg line; ok is false for anything that is not a `bind`.
func parseBindLine(l string) (Keybind, bool) {
	m := reBindLine.FindStringSubmatch(l)
	if m == nil || m[3] == "" {
		return Keybind{}, false
	}
	cmd := m[3]
	if len(cmd) >= 2 && cmd[0] == '"' && cmd[len(cmd)-1] == '"' {
		cmd = cmd[1 : len(cmd)-1]
	}
	return Keybind{Mapper: strings.ToUpper(m[1]), Key: strings.ToUpper(m[2]), Command: cmd}, true
}

// parseKeybinds returns the user binds in the cfg text, in file order.
func parseKeybinds(text string) []Keybind {
	lines, _ := splitCfg(text)
	out := []Keybind{}
	for _, l := range lines {
		if b, ok := parseBindLine(l); ok {
			out = append(out, b)
		}
	}
	return out
}

// validKeybind trims and checks one bind so it can be written safely as a cfg line.
func validKeybind(b Keybind) (Keybind, error) {
	b.Mapper = strings.ToUpper(strings.TrimSpace(b.Mapper))
	b.Key = strings.ToUpper(strings.TrimSpace(b.Key))
	b.Command = strings.TrimSpace(b.Command)
	if b.Mapper == "" {
		b.Mapper = "KEYBOARD"
	}
	if !reBindToken.MatchString(b.Mapper) {
		return b, fmt.Errorf("jenis input %q tidak valid", b.Mapper)
	}
	if !reBindToken.MatchString(b.Key) {
		return b, fmt.Errorf("tombol %q tidak valid (huruf, angka, dan _ saja, contoh: I, F5, NUMPAD0)", b.Key)
	}
	if b.Command == "" {
		return b, fmt.Errorf("perintah untuk tombol %s kosong", b.Key)
	}
	if utf8.RuneCountInString(b.Command) > maxBindCmdLen {
		return b, fmt.Errorf("perintah untuk tombol %s terlalu panjang (maksimal %d karakter)", b.Key, maxBindCmdLen)
	}
	for _, r := range b.Command {
		if unicode.IsControl(r) || r == '"' || r == '\\' || r == ';' {
			return b, fmt.Errorf(`perintah untuk tombol %s tidak boleh berisi tanda kutip ("), garis miring terbalik (\), titik koma (;), atau karakter kontrol`, b.Key)
		}
	}
	return b, nil
}

// validKeybinds checks the whole list; the same mapper and key twice is refused because
// only the last one would take effect.
func validKeybinds(in []Keybind) ([]Keybind, error) {
	if len(in) > maxKeybinds {
		return nil, fmt.Errorf("terlalu banyak keybind (maksimal %d)", maxKeybinds)
	}
	out := make([]Keybind, 0, len(in))
	seen := map[string]bool{}
	for _, b := range in {
		v, err := validKeybind(b)
		if err != nil {
			return nil, err
		}
		if !v.Disabled { // disabled binds are not in fivem.cfg, so they may share a key
			id := v.Mapper + " " + v.Key
			if seen[id] {
				return nil, fmt.Errorf("tombol %s dipakai dua kali. Satu tombol hanya boleh punya satu perintah aktif", v.Key)
			}
			seen[id] = true
		}
		out = append(out, v)
	}
	return out, nil
}

func bindLine(b Keybind) string {
	return `bind ` + b.Mapper + ` ` + b.Key + ` "` + b.Command + `"`
}

// applyKeybinds returns the cfg text with its `bind` lines replaced by binds. The new
// lines go where the first old bind was (or right after `unbindall`, or at the end), so
// they always come after unbindall. Every other line, rbind included, is kept as is.
func applyKeybinds(text string, binds []Keybind) string {
	lines, eol := splitCfg(text)
	news := make([]string, len(binds))
	for i, b := range binds {
		news[i] = bindLine(b)
	}
	out := make([]string, 0, len(lines)+len(news))
	at := -1
	for _, l := range lines {
		if _, ok := parseBindLine(l); ok {
			if at < 0 {
				at = len(out)
			}
			continue
		}
		out = append(out, l)
	}
	if at < 0 {
		at = len(out)
		for i, l := range out {
			if strings.EqualFold(strings.TrimSpace(l), "unbindall") {
				at = i + 1
				break
			}
		}
	}
	res := make([]string, 0, len(out)+len(news))
	res = append(res, out[:at]...)
	res = append(res, news...)
	res = append(res, out[at:]...)
	if len(res) == 0 {
		return ""
	}
	return strings.Join(res, eol) + eol
}

// GetKeybinds lists the `bind` lines of fivem.cfg, then the binds disabled in the launcher.
func (a *App) GetKeybinds() KeybindList {
	res := a.cfgKeybinds()
	for _, d := range a.cfg.get().DisabledBinds {
		d.Disabled = true
		res.Items = append(res.Items, d)
	}
	return res
}

func (a *App) cfgKeybinds() KeybindList {
	path, err := fivemCfgPath()
	if err != nil {
		return KeybindList{Items: []Keybind{}, Error: err.Error()}
	}
	res := KeybindList{OK: true, Items: []Keybind{}, Path: path, Running: fivemRunning()}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			res.OK, res.Error = false, err.Error()
		}
		return res
	}
	res.Exists = true
	res.Items = parseKeybinds(string(b))
	return res
}

// SaveKeybinds writes the enabled binds to fivem.cfg and keeps the disabled ones in the
// launcher config, so they can be switched on again later.
func (a *App) SaveKeybinds(items []Keybind) SettingsResult {
	if !a.allowed() {
		return SettingsResult{Msg: a.lockedMsg()}
	}
	binds, err := validKeybinds(items)
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	if fivemRunning() {
		return SettingsResult{Msg: "FiveM sedang terbuka. Tutup FiveM sepenuhnya dulu, karena FiveM menulis ulang fivem.cfg saat ditutup."}
	}
	var on, off []Keybind
	for _, b := range binds {
		if b.Disabled {
			off = append(off, b)
		} else {
			on = append(on, b)
		}
	}
	path, err := fivemCfgPath()
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	orig, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return SettingsResult{Msg: "Gagal membaca fivem.cfg: " + err.Error()}
	}
	next := applyKeybinds(string(orig), on)
	if (exists && next != string(orig)) || (!exists && len(on) > 0) {
		if err := commitCfg(path, orig, exists, next); err != nil {
			return SettingsResult{Msg: "Gagal menulis fivem.cfg: " + err.Error()}
		}
	}
	a.cfg.update(func(c *Config) { c.DisabledBinds = off })
	return SettingsResult{OK: true, Msg: fmt.Sprintf("Tersimpan: %d aktif, %d nonaktif.", len(on), len(off))}
}
