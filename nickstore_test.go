package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
)

func TestDecodeLocalStorageValue(t *testing.T) {
	if got := decodeLocalStorageValue(append([]byte{1}, "NAMA NYA DISINI"...)); got != "NAMA NYA DISINI" {
		t.Errorf("latin1: %q", got)
	}
	if got := decodeLocalStorageValue(append([]byte{1}, 0xE9)); got != "é" {
		t.Errorf("latin1 high byte: %q", got)
	}
	utf16le := []byte{0, 'H', 0, 0x3D, 0xD8, 0x00, 0xDE} // "H" and U+1F600
	if got := decodeLocalStorageValue(utf16le); got != "H\U0001F600" {
		t.Errorf("utf16: %q", got)
	}
	if decodeLocalStorageValue(nil) != "" {
		t.Error("empty value")
	}
}

// makeStorage builds a Local Storage style LevelDB the way Chromium lays it out.
func makeStorage(t *testing.T, root, name string, kv map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(root, "data", "nui-storage", name, "Local Storage", "leveldb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range kv {
		if err := db.Put([]byte(k), v, nil); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return dir
}

func TestReadNickOverride(t *testing.T) {
	root := t.TempDir()
	key := "_" + nuiOrigin + "\x00\x01" + nickOverrideKey
	dir := makeStorage(t, root, "context-server_111", map[string][]byte{
		key:                                  append([]byte{1}, "NAMA NYA DISINI"...),
		"_" + nuiOrigin + "\x00\x01clientId": append([]byte{1}, "abc"...),
		"_https://other.example\x00\x01" + nickOverrideKey: append([]byte{1}, "WRONG ORIGIN"...),
	})
	before, _ := os.ReadDir(dir)

	v, d, ok := readNickOverride(root)
	if !ok || v != "NAMA NYA DISINI" || d != dir {
		t.Fatalf("got %q %q %v", v, d, ok)
	}
	// the original folder must be untouched (same files, no LOCK taken, nothing written)
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Errorf("files changed: %d -> %d", len(before), len(after))
	}
}

func TestReadNickOverrideMissing(t *testing.T) {
	root := t.TempDir()
	makeStorage(t, root, "context-server_222", map[string][]byte{
		"_" + nuiOrigin + "\x00\x01clientId": append([]byte{1}, "abc"...),
	})
	if _, _, ok := readNickOverride(root); ok {
		t.Error("a store without nickOverride must report not found")
	}
	if _, _, ok := readNickOverride(filepath.Join(root, "does-not-exist")); ok {
		t.Error("a missing folder must report not found")
	}
}

func TestReadNickOverridePicksNewestProfile(t *testing.T) {
	root := t.TempDir()
	key := "_" + nuiOrigin + "\x00\x01" + nickOverrideKey
	old := makeStorage(t, root, "context-server_1", map[string][]byte{key: append([]byte{1}, "LAMA"...)})
	makeStorage(t, root, "context-server_2", map[string][]byte{key: append([]byte{1}, "BARU"...)})
	// make profile 1 clearly older
	entries, _ := os.ReadDir(old)
	for _, e := range entries {
		past := testTime(-48)
		os.Chtimes(filepath.Join(old, e.Name()), past, past)
	}
	if v, _, ok := readNickOverride(root); !ok || v != "BARU" {
		t.Errorf("want the newest profile, got %q %v", v, ok)
	}
}

func testTime(hoursFromNow int) time.Time {
	return time.Now().Add(time.Duration(hoursFromNow) * time.Hour)
}

// dumpStore returns every key/value of a LevelDB folder, read from a copy.
func dumpStore(t *testing.T, dir string) map[string]string {
	t.Helper()
	tmp := t.TempDir()
	if err := copyDirFiles(dir, tmp); err != nil {
		t.Fatal(err)
	}
	db, err := leveldb.OpenFile(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]string{}
	it := db.NewIterator(nil, nil)
	defer it.Release()
	for it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	return out
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func seedStore(t *testing.T, root, profile, nick string) (dir string, key string) {
	t.Helper()
	key = "_" + nuiOrigin + "\x00\x01" + nickOverrideKey
	kv := map[string][]byte{
		"_" + nuiOrigin + "\x00\x01clientId":        append([]byte{1}, "abc"...),
		"_" + nuiOrigin + "\x00\x01legalAcceptance": append([]byte{1}, "yes"...),
		"META:" + nuiOrigin:                         {8, 1},
	}
	if nick != "" {
		kv[key] = append([]byte{1}, nick...)
	}
	return makeStorage(t, root, profile, kv), key
}

func TestEncodeLocalStorageValueRoundTrip(t *testing.T) {
	for _, s := range []string{"Budi Santoso", "é", "NAMA NYA DISINI", "Rina \U0001F600", "日本語"} {
		if got := decodeLocalStorageValue(encodeLocalStorageValue(s)); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
	if b := encodeLocalStorageValue("abc"); b[0] != 1 {
		t.Error("plain text must use the Latin-1 marker like Chromium")
	}
	if b := encodeLocalStorageValue("日"); b[0] != 0 {
		t.Error("wide text must use the UTF-16 marker")
	}
}

func TestWriteNickOverride(t *testing.T) {
	root := t.TempDir()
	dir, key := seedStore(t, root, "context-server_1", "NAMA LAMA")
	before := dumpStore(t, dir)

	if err := writeNickOverride(root, "Budi Santoso"); err != nil {
		t.Fatal(err)
	}
	after := dumpStore(t, dir)
	if after[key] != "\x01Budi Santoso" {
		t.Errorf("value = %q", after[key])
	}
	for k, v := range before {
		if k != key && after[k] != v {
			t.Errorf("other key %q changed: %q -> %q", k, v, after[k])
		}
	}
	if len(after) != len(before) {
		t.Errorf("key count changed: %d -> %d", len(before), len(after))
	}
	parent := filepath.Dir(dir)
	if got := dumpStore(t, filepath.Join(parent, "leveldb.imebak")); !sameMap(got, before) {
		t.Error("leveldb.imebak must hold the original data")
	}
	if _, err := os.Stat(filepath.Join(parent, "leveldb.imebak-run")); err == nil {
		t.Error("the per-save backup must be removed after a successful write")
	}

	// a second save must not overwrite the one-time original backup
	if err := writeNickOverride(root, "Siti \U0001F600"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := readNickOverride(root); v != "Siti \U0001F600" {
		t.Errorf("emoji name read back as %q", v)
	}
	if got := dumpStore(t, filepath.Join(parent, "leveldb.imebak")); !sameMap(got, before) {
		t.Error("leveldb.imebak must stay the original after later saves")
	}
}

func TestWriteNickOverrideRemoves(t *testing.T) {
	root := t.TempDir()
	dir, key := seedStore(t, root, "context-server_1", "NAMA LAMA")
	if err := writeNickOverride(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := dumpStore(t, dir)[key]; ok {
		t.Error("an empty name must remove the key")
	}
	if _, _, ok := readNickOverride(root); ok {
		t.Error("reader must report not found after removal")
	}
}

func TestWriteNickOverrideAddsMissingKey(t *testing.T) {
	root := t.TempDir()
	dir, key := seedStore(t, root, "context-server_1", "") // FiveM UI data, but no player name yet
	if err := writeNickOverride(root, "Baru"); err != nil {
		t.Fatal(err)
	}
	if dumpStore(t, dir)[key] != "\x01Baru" {
		t.Error("key must be added")
	}
}

func TestWriteNickOverrideRollsBack(t *testing.T) {
	root := t.TempDir()
	dir, _ := seedStore(t, root, "context-server_1", "NAMA LAMA")
	before := dumpStore(t, dir)

	old := verifyOverride
	verifyOverride = func(string) (string, bool) { return "something else", true }
	defer func() { verifyOverride = old }()

	err := writeNickOverride(root, "Budi")
	if err == nil || !strings.Contains(err.Error(), "dikembalikan") {
		t.Fatalf("expected a rollback error, got %v", err)
	}
	if got := dumpStore(t, dir); !sameMap(got, before) {
		t.Error("store must be restored to the original data")
	}
}

func TestWriteNickOverrideRefusesLockedStore(t *testing.T) {
	root := t.TempDir()
	dir, _ := seedStore(t, root, "context-server_1", "NAMA LAMA")
	before := dumpStore(t, dir)

	held, err := leveldb.OpenFile(dir, nil) // stands in for a running FiveM
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err := writeNickOverrideDir(dir, "Budi"); err == nil {
		t.Fatal("writing to a locked store must fail")
	}
	held.Close()
	if got := dumpStore(t, dir); !sameMap(got, before) {
		t.Error("a refused write must not change the data")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "leveldb.imebak-run")); err == nil {
		t.Error("no per-save backup may be left behind after a refused write")
	}
}

func TestWriteNickOverrideNoStore(t *testing.T) {
	if err := writeNickOverride(t.TempDir(), "Budi"); !errors.Is(err, errNoNickStore) {
		t.Errorf("want errNoNickStore, got %v", err)
	}
}
