package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Setting is one key written to the [Game] section of CitizenFX.ini.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func defaultFiveMSettings() []Setting {
	return []Setting{
		{"PoolSizesIncrease", `{"AnimStore":20480,"CMoveObject":75,"FragmentStore":14000,"OcclusionInteriorInfo":20,"OcclusionPathNode":5000,"OcclusionPortalEntity":750,"OcclusionPortalInfo":750,"PortalInst":225,"ScaleformStore":200,"StaticBounds":5000,"TxdStore":26000}`},
		{"DefaultBuild", "3889"},
		{"SavedBuildNumber", "2612"},
	}
}

// retiredFiveMValues are values earlier launcher versions wrote into config.json. The
// config keeps a copy of the settings, so a saved old default would otherwise win over
// the new one forever; values the player edited by hand are left alone.
var retiredFiveMValues = map[string][]string{
	"DefaultBuild": {"3258"},
}

// migrateFiveMSettings replaces retired default values with the current ones.
func migrateFiveMSettings(saved, current []Setting) []Setting {
	out := make([]Setting, len(saved))
	copy(out, saved)
	for i, s := range out {
		for _, old := range retiredFiveMValues[s.Key] {
			if s.Value != old {
				continue
			}
			for _, c := range current {
				if c.Key == s.Key {
					out[i].Value = c.Value
				}
			}
		}
	}
	return out
}

// fivemRunning reports whether FiveM.exe is in the process list (no child process spawned).
// fivemRunning is a var so tests can stub it.
var fivemRunning = isFivemRunning

func isFivemRunning() bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "FiveM.exe") {
			return true
		}
	}
	return false
}

// applyFiveMSettings makes sure CitizenFX.ini in the FiveM Application Data folder
// holds the given [Game] keys. Other keys and sections are left untouched. It returns
// true when the file was changed (a one-time .imebak copy of the original is kept).
func applyFiveMSettings(appDir string, settings []Setting) (bool, error) {
	if appDir == "" {
		return false, errors.New("folder FiveM Application Data belum dipilih")
	}
	path := filepath.Join(appDir, "CitizenFX.ini")
	if entries, err := os.ReadDir(appDir); err == nil { // keep the file's existing letter case
		for _, e := range entries {
			if strings.EqualFold(e.Name(), "CitizenFX.ini") {
				path = filepath.Join(appDir, e.Name())
			}
		}
	}

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

	section := func(ls []string) (start, end int) { // [Game] body range
		start, end = -1, len(ls)
		for i, l := range ls {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "[") {
				if start >= 0 {
					return start, i
				}
				if strings.EqualFold(t, "[Game]") {
					start = i + 1
				}
			}
		}
		return start, end
	}
	if s, _ := section(lines); s < 0 {
		lines = append(lines, "[Game]")
	}

	changed := false
	for _, st := range settings {
		s, e := section(lines)
		found := false
		for i := s; i < e; i++ {
			k, v, ok := strings.Cut(lines[i], "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), st.Key) {
				found = true
				if strings.TrimSpace(v) != st.Value {
					lines[i] = st.Key + "=" + st.Value
					changed = true
				}
				break
			}
		}
		if !found {
			at := e
			for at > s && strings.TrimSpace(lines[at-1]) == "" {
				at--
			}
			lines = append(lines[:at], append([]string{st.Key + "=" + st.Value}, lines[at:]...)...)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}

	if exists {
		if _, err := os.Stat(path + ".imebak"); os.IsNotExist(err) {
			_ = os.WriteFile(path+".imebak", orig, 0o644)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, eol)+eol), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
