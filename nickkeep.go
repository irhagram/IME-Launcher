package main

import "fmt"

// KeepNickname saves the name in the launcher config. Connect then sets it before FiveM
// starts, so it holds even if the name is changed in FiveM later.
func (a *App) KeepNickname(name string) SettingsResult {
	if !a.allowed() {
		return SettingsResult{Msg: a.lockedMsg()}
	}
	name, err := validNickname(name)
	if err != nil {
		return SettingsResult{Msg: err.Error()}
	}
	a.cfg.update(func(c *Config) { c.KeptNick = name })
	return SettingsResult{OK: true, Msg: fmt.Sprintf("Nickname %q disimpan untuk IME. Dipasang otomatis saat Connect.", name)}
}

// RemoveKeptNickname forgets the kept name. The name already in FiveM stays as it is.
func (a *App) RemoveKeptNickname() SettingsResult {
	if !a.allowed() {
		return SettingsResult{Msg: a.lockedMsg()}
	}
	a.cfg.update(func(c *Config) { c.KeptNick = "" })
	return SettingsResult{OK: true, Msg: "Keep for IME dihapus."}
}

// applyKeptNickname makes FiveM use the kept name. It returns a short note for the
// Connect message, or "" when there is nothing to say (no kept name, or already in place).
// It never blocks the connection.
func (a *App) applyKeptNickname() string {
	kept := a.cfg.get().KeptNick
	if kept == "" {
		return ""
	}
	kept, err := validNickname(kept)
	if err != nil {
		return "Nickname Keep tidak valid: " + err.Error()
	}
	if info := a.GetNickname(); info.OK && info.Value == kept && info.CfgValue == kept {
		return ""
	}
	if fivemRunning() {
		return fmt.Sprintf("Nickname %q belum dipasang karena FiveM sedang terbuka.", kept)
	}
	if r := a.writeNickname(kept); !r.OK {
		return "Nickname Keep gagal dipasang: " + r.Msg
	}
	return fmt.Sprintf("Nickname diatur ke %q.", kept)
}
