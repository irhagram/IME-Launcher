package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingValue(list []Setting, key string) string {
	for _, s := range list {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

func TestDefaultBuildSettings(t *testing.T) {
	d := defaultFiveMSettings()
	if settingValue(d, "DefaultBuild") != "3889" || settingValue(d, "SavedBuildNumber") != "2612" {
		t.Errorf("unexpected defaults: %+v", d)
	}
}

func TestMigrateFiveMSettings(t *testing.T) {
	cur := defaultFiveMSettings()
	saved := []Setting{{"DefaultBuild", "3258"}, {"SavedBuildNumber", "2612"}, {"Custom", "1"}}
	got := migrateFiveMSettings(saved, cur)
	if settingValue(got, "DefaultBuild") != "3889" {
		t.Errorf("retired build must move to the new one, got %q", settingValue(got, "DefaultBuild"))
	}
	if settingValue(got, "Custom") != "1" || settingValue(saved, "DefaultBuild") != "3258" {
		t.Error("other keys must stay and the input must not be modified")
	}
	// a value the player chose on purpose is not retired
	hand := migrateFiveMSettings([]Setting{{"DefaultBuild", "2802"}}, cur)
	if settingValue(hand, "DefaultBuild") != "2802" {
		t.Errorf("hand-edited value must stay, got %q", settingValue(hand, "DefaultBuild"))
	}
}

func TestConfigStoreMigratesSavedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"fivemSettings":[{"key":"DefaultBuild","value":"3258"},{"key":"SavedBuildNumber","value":"2612"}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newConfigStore(path)
	if v := settingValue(s.get().FiveMSettings, "DefaultBuild"); v != "3889" {
		t.Errorf("saved config still has DefaultBuild=%s", v)
	}
}

func TestApplyFiveMSettingsUpdatesBuild(t *testing.T) {
	dir := t.TempDir()
	ini := "[Game]\r\nDefaultBuild=3258\r\nSavedBuildNumber=2612\r\nOther=keep\r\n[Other]\r\nX=1\r\n"
	path := filepath.Join(dir, "CitizenFX.ini")
	os.WriteFile(path, []byte(ini), 0o644)

	changed, err := applyFiveMSettings(dir, []Setting{{"DefaultBuild", "3889"}, {"SavedBuildNumber", "2612"}})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	for _, want := range []string{"DefaultBuild=3889", "SavedBuildNumber=2612", "Other=keep", "[Other]", "X=1"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "3258") {
		t.Errorf("old build still present:\n%s", text)
	}
	if bak, _ := os.ReadFile(path + ".imebak"); string(bak) != ini {
		t.Error(".imebak must hold the original file")
	}
	if again, _ := applyFiveMSettings(dir, []Setting{{"DefaultBuild", "3889"}}); again {
		t.Error("a second run with the same values must not change the file")
	}
}

func TestApplyFiveMSettingsCreatesMissingKeys(t *testing.T) {
	dir := t.TempDir()
	changed, err := applyFiveMSettings(dir, []Setting{{"DefaultBuild", "3889"}, {"SavedBuildNumber", "2612"}})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "CitizenFX.ini"))
	if !strings.Contains(string(b), "[Game]") || !strings.Contains(string(b), "DefaultBuild=3889") {
		t.Errorf("new ini incomplete:\n%s", b)
	}
}
