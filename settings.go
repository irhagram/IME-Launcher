package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNickname = 30

// reNickname matches the line that sets the nickname convar in fivem.cfg.
var reNickname = regexp.MustCompile(`(?i)^\s*(?:seta|sets|set)\s+"?cl_nickname"?(?:\s|$)`)

// NicknameInfo is what the settings tab shows.
type NicknameInfo struct {
	OK       bool   `json:"ok"`
	Value    string `json:"value"`  // the name to show: FiveM's own setting if it has one, else cl_nickname
	Source   string `json:"source"` // where Value came from
	Path     string `json:"path"`   // file or folder it was read from
	CfgValue string `json:"cfgValue"`
	Exists   bool   `json:"exists"`
	Running  bool   `json:"running"` // FiveM is open: it rewrites fivem.cfg when it closes
	Error    string `json:"error,omitempty"`
}

// SettingsResult is the outcome of saving a setting.
type SettingsResult struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

// fivemCfgPath is %AppData%\CitizenFX\fivem.cfg, where FiveM keeps its console variables.
func fivemCfgPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "CitizenFX", "fivem.cfg"), nil
}

// validNickname trims the name and rejects what cannot be written safely inside the
// quoted value of a cfg line.
func validNickname(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "", errors.New("nickname kosong")
	}
	if utf8.RuneCountInString(n) > maxNickname {
		return "", errors.New("nickname terlalu panjang (maksimal 30 karakter)")
	}
	for _, r := range n {
		if unicode.IsControl(r) || r == '"' || r == '\\' || r == ';' {
			return "", errors.New(`nickname tidak boleh berisi tanda kutip ("), garis miring terbalik (\), titik koma (;), atau karakter kontrol`)
		}
	}
	return n, nil
}

// readNickname returns the cl_nickname value in the cfg text, if the line exists.
func readNickname(text string) (string, bool) {
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if !reNickname.MatchString(l) {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(l), `"`, 5) // seta "cl_nickname" "value"
		if len(fields) >= 4 {
			return fields[3], true
		}
		return "", true
	}
	return "", false
}

// setNicknameInFile writes `seta "cl_nickname" "<name>"` into the cfg at path, replacing
// an existing line (or adding one at the end). An empty name removes the line. Other
// lines are kept as they are, a one-time .imebak copy of the original is made, and the
// file is replaced atomically. It reports whether the file changed.
func setNicknameInFile(path, name string) (bool, error) {
	orig, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	text := string(orig)
	eol := "\r\n"
	if exists && !strings.Contains(text, "\r\n") && strings.Contains(text, "\n") {
		eol = "\n"
	}
	var lines []string
	if text != "" {
		lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	want := `seta "cl_nickname" "` + name + `"`
	out := make([]string, 0, len(lines)+1)
	placed := false
	for _, l := range lines {
		if !reNickname.MatchString(l) {
			out = append(out, l)
			continue
		}
		if name != "" && !placed { // first match is rewritten, repeats are dropped
			out = append(out, want)
			placed = true
		}
	}
	if name != "" && !placed {
		out = append(out, want)
	}
	next := ""
	if len(out) > 0 {
		next = strings.Join(out, eol) + eol
	}
	if exists && next == text {
		return false, nil
	}
	if !exists && name == "" {
		return false, nil
	}

	if exists {
		if _, err := os.Stat(path + ".imebak"); os.IsNotExist(err) {
			_ = os.WriteFile(path+".imebak", orig, 0o644)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// GetNickname reads the current cl_nickname from fivem.cfg.
func (a *App) GetNickname() NicknameInfo {
	path, err := fivemCfgPath()
	if err != nil {
		return NicknameInfo{Error: err.Error()}
	}
	info := NicknameInfo{OK: true, Path: path, Source: "fivem.cfg", Running: fivemRunning()}
	if b, err := os.ReadFile(path); err == nil {
		info.Exists = true
		info.CfgValue, _ = readNickname(string(b))
		info.Value = info.CfgValue
	}
	if v, dir, ok := a.fivemOwnName(); ok {
		info.Value, info.Source, info.Path = v, "Settings FiveM", dir
	}
	return info
}

// fivemOwnName is the Player Name saved in FiveM's own settings page, if there is one.
func (a *App) fivemOwnName() (name, dir string, ok bool) {
	app := a.LocateFiveM()
	if app == "" {
		return "", "", false
	}
	name, dir, ok = readNickOverride(app)
	return name, dir, ok && strings.TrimSpace(name) != ""
}

// SetNickname saves the nickname to fivem.cfg; an empty name removes the setting.
func (a *App) SetNickname(name string) SettingsResult {
	if !a.allowed() {
		return SettingsResult{Msg: a.lockedMsg()}
	}
	path, err := fivemCfgPath()
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	if strings.TrimSpace(name) != "" {
		if name, err = validNickname(name); err != nil {
			return SettingsResult{Msg: err.Error()}
		}
	} else {
		name = ""
	}
	if fivemRunning() {
		return SettingsResult{Msg: "FiveM sedang terbuka. Tutup FiveM sepenuhnya dulu, karena FiveM menulis ulang fivem.cfg saat ditutup."}
	}
	_, err = setNicknameInFile(path, name)
	if err != nil {
		return SettingsResult{Msg: "Gagal menulis fivem.cfg: " + err.Error()}
	}
	// FiveM's own Player Name wins over cl_nickname, so keep it in step.
	app := a.LocateFiveM()
	if app == "" {
		return SettingsResult{OK: true, Msg: "Tersimpan ke fivem.cfg. Folder FiveM belum diatur, jadi Settings FiveM tidak diubah."}
	}
	switch err := writeNickOverride(app, name); {
	case errors.Is(err, errNoNickStore):
		return SettingsResult{OK: true, Msg: "Tersimpan ke fivem.cfg. Settings FiveM belum punya data, jadi tidak diubah."}
	case err != nil:
		return SettingsResult{Msg: "fivem.cfg tersimpan, tapi Settings FiveM gagal diubah: " + err.Error()}
	}
	if name == "" {
		return SettingsResult{OK: true, Msg: "Nickname dihapus dari fivem.cfg dan Settings FiveM."}
	}
	return SettingsResult{OK: true, Msg: "Tersimpan ke fivem.cfg dan Settings FiveM."}
}
