package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"golang.org/x/sys/windows"
)

// FiveM keeps the "Player Name" of its settings page in the Local Storage of its
// embedded browser: origin https://nui-game-internal, key nickOverride. Local Storage
// is a LevelDB, written as key "_<origin>\x00<flag>nickOverride" and value
// "<enc><text>" where enc 1 means Latin-1 bytes and enc 0 means UTF-16LE.
const (
	nuiOrigin       = "https://nui-game-internal"
	nickOverrideKey = "nickOverride"
)

// readNickOverride returns FiveM's own player name setting, looking in every
// nui-storage\context-server_* profile of the FiveM Application Data folder, newest
// first. It only reads: the database is copied to a temp folder and opened there, so
// FiveM can stay open and its files are never touched.
func readNickOverride(fivemApp string) (value, dir string, found bool) {
	pattern := filepath.Join(fivemApp, "data", "nui-storage", "context-server_*", "Local Storage", "leveldb")
	dirs, _ := filepath.Glob(pattern)
	type cand struct {
		dir string
		mod time.Time
	}
	var cands []cand
	for _, d := range dirs {
		var newest time.Time
		if entries, err := os.ReadDir(d); err == nil {
			for _, e := range entries {
				if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
					newest = fi.ModTime()
				}
			}
		}
		cands = append(cands, cand{d, newest})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		if v, ok := readOverrideFromDir(c.dir); ok {
			return v, c.dir, true
		}
	}
	return "", "", false
}

func readOverrideFromDir(dir string) (string, bool) {
	tmp, err := os.MkdirTemp("", "imenick")
	if err != nil {
		return "", false
	}
	defer os.RemoveAll(tmp)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if copyFile(filepath.Join(dir, e.Name()), filepath.Join(tmp, e.Name())) != nil {
			return "", false
		}
	}
	db, err := leveldb.OpenFile(tmp, nil)
	if err != nil {
		if db, err = leveldb.RecoverFile(tmp, nil); err != nil {
			return "", false
		}
	}
	defer db.Close()

	prefix := []byte("_" + nuiOrigin + "\x00")
	it := db.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()
	for it.Next() {
		k := it.Key()[len(prefix):] // flag byte, then the name
		if len(k) > 1 && string(k[1:]) == nickOverrideKey {
			return decodeLocalStorageValue(it.Value()), true
		}
	}
	return "", false
}

// openShared opens a file for reading while another process (FiveM's browser) has it
// open for writing: os.Open does not allow every sharing mode combination.
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func copyFile(src, dst string) error {
	in, err := openShared(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// decodeLocalStorageValue turns Chromium's stored string into Go text.
func decodeLocalStorageValue(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	body := b[1:]
	switch b[0] {
	case 0: // UTF-16LE
		u := make([]uint16, 0, len(body)/2)
		for i := 0; i+1 < len(body); i += 2 {
			u = append(u, uint16(body[i])|uint16(body[i+1])<<8)
		}
		return string(utf16.Decode(u))
	case 1: // Latin-1
		r := make([]rune, 0, len(body))
		for _, c := range body {
			r = append(r, rune(c))
		}
		return string(r)
	}
	return string(body)
}

// ---- writing ----

// errNoNickStore means FiveM has no Local Storage profile to write to (yet).
var errNoNickStore = errors.New("penyimpanan Settings FiveM tidak ditemukan")

// verifyOverride re-reads the value after a write; tests replace it.
var verifyOverride = readOverrideFromDir

// profileDirs lists nui-storage Local Storage folders, newest first.
func profileDirs(fivemApp string) []string {
	pattern := filepath.Join(fivemApp, "data", "nui-storage", "context-server_*", "Local Storage", "leveldb")
	dirs, _ := filepath.Glob(pattern)
	type cand struct {
		dir string
		mod time.Time
	}
	cands := make([]cand, 0, len(dirs))
	for _, d := range dirs {
		var newest time.Time
		if entries, err := os.ReadDir(d); err == nil {
			for _, e := range entries {
				if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
					newest = fi.ModTime()
				}
			}
		}
		cands = append(cands, cand{d, newest})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.dir
	}
	return out
}

// hasOrigin reports whether the profile already holds data of FiveM's own UI.
func hasOrigin(dir string) bool {
	tmp, err := os.MkdirTemp("", "imenick")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmp)
	if copyDirFiles(dir, tmp) != nil {
		return false
	}
	db, err := leveldb.OpenFile(tmp, nil)
	if err != nil {
		if db, err = leveldb.RecoverFile(tmp, nil); err != nil {
			return false
		}
	}
	defer db.Close()
	it := db.NewIterator(util.BytesPrefix([]byte("_"+nuiOrigin+"\x00")), nil)
	defer it.Release()
	return it.Next()
}

func copyDirFiles(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// clearDirFiles removes the regular files of dir (used before restoring a backup).
func clearDirFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// encodeLocalStorageValue is the inverse of decodeLocalStorageValue: Latin-1 when every
// character fits in a byte, UTF-16LE otherwise (what Chromium does).
func encodeLocalStorageValue(s string) []byte {
	latin := true
	for _, r := range s {
		if r > 0xFF {
			latin = false
			break
		}
	}
	if latin {
		out := []byte{1}
		for _, r := range s {
			out = append(out, byte(r))
		}
		return out
	}
	out := []byte{0}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// writeNickOverride sets (or, for an empty name, removes) FiveM's own Player Name in
// its Local Storage. FiveM must be closed: the database is locked while it runs, and a
// running FiveM would overwrite the change from memory. The folder is backed up before
// the write and restored if the value cannot be read back. It returns errNoNickStore
// when FiveM has no profile with its UI data yet.
func writeNickOverride(fivemApp, name string) error {
	var dir string
	for _, d := range profileDirs(fivemApp) {
		if _, ok := readOverrideFromDir(d); ok || hasOrigin(d) {
			dir = d
			break
		}
	}
	if dir == "" {
		return errNoNickStore
	}
	return writeNickOverrideDir(dir, name)
}

func writeNickOverrideDir(dir, name string) error {
	parent := filepath.Dir(dir)
	once := filepath.Join(parent, "leveldb.imebak") // the untouched original, made once
	run := filepath.Join(parent, "leveldb.imebak-run")
	for _, b := range []string{once, run} {
		if _, err := os.Stat(b); b == run || os.IsNotExist(err) {
			os.RemoveAll(b)
			if err := os.MkdirAll(b, 0o755); err != nil {
				return err
			}
			if err := copyDirFiles(dir, b); err != nil {
				os.RemoveAll(b)
				return fmt.Errorf("gagal membuat cadangan: %w", err)
			}
		}
	}

	restore := func(cause error) error {
		if err := clearDirFiles(dir); err == nil {
			if err = copyDirFiles(run, dir); err == nil {
				os.RemoveAll(run)
				return fmt.Errorf("%w (penyimpanan dikembalikan seperti semula)", cause)
			}
		}
		return fmt.Errorf("%w (GAGAL mengembalikan otomatis, cadangan ada di %s)", cause, run)
	}

	db, err := leveldb.OpenFile(dir, &opt.Options{ErrorIfMissing: true})
	if err != nil {
		os.RemoveAll(run) // nothing was changed
		return fmt.Errorf("tidak bisa membuka penyimpanan (FiveM masih terbuka?): %w", err)
	}
	key := []byte("_" + nuiOrigin + "\x00\x01" + nickOverrideKey)
	if name == "" {
		err = db.Delete(key, &opt.WriteOptions{Sync: true})
	} else {
		err = db.Put(key, encodeLocalStorageValue(name), &opt.WriteOptions{Sync: true})
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return restore(err)
	}

	got, found := verifyOverride(dir)
	if (name == "" && found) || (name != "" && (!found || got != name)) {
		return restore(errors.New("nilai tidak terbaca kembali setelah ditulis"))
	}
	os.RemoveAll(run)
	return nil
}
