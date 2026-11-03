package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.2", -1},
		{"1.0.10", "1.0.2", 1},
		{"v1.2", "1.2.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-beta", "1.0.0", 0},
		{"", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func doc(t *testing.T, js string) remoteDoc {
	t.Helper()
	var d remoteDoc
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name, cur, js string
		state         string
		blocked       bool
	}{
		{"ok", "1.0.0", `{"launcher":{"enabled":true,"minimum_version":"1.0.0","latest_version":"1.0.0"}}`, "ok", false},
		{"soft update", "1.0.0", `{"launcher":{"enabled":true,"minimum_version":"1.0.0","latest_version":"1.0.2"}}`, "update", false},
		{"below minimum", "1.0.0", `{"launcher":{"minimum_version":"1.0.1","latest_version":"1.0.2"}}`, "force", true},
		{"force flag", "1.0.0", `{"launcher":{"minimum_version":"1.0.0","latest_version":"1.0.2","force_update":true}}`, "force", true},
		{"force flag but current", "1.0.2", `{"launcher":{"latest_version":"1.0.2","force_update":true}}`, "ok", false},
		{"maintenance", "1.0.0", `{"launcher":{"maintenance":true,"latest_version":"1.0.0"}}`, "maintenance", true},
		{"disabled", "1.0.0", `{"launcher":{"enabled":false}}`, "disabled", true},
		{"disabled beats maintenance", "1.0.0", `{"launcher":{"enabled":false,"maintenance":true}}`, "disabled", true},
		{"maintenance beats update", "1.0.0", `{"launcher":{"maintenance":true,"minimum_version":"2.0.0"}}`, "maintenance", true},
		{"enabled missing means on", "1.0.0", `{"launcher":{"latest_version":"1.0.0"}}`, "ok", false},
	}
	for _, c := range cases {
		st := decide(c.cur, doc(t, c.js))
		if st.State != c.state || st.Blocked != c.blocked {
			t.Errorf("%s: got state=%s blocked=%v, want %s %v", c.name, st.State, st.Blocked, c.state, c.blocked)
		}
	}
}

func TestMessageAndDownloadURL(t *testing.T) {
	st := decide("1.0.0", doc(t, `{"launcher":{"minimum_version":"2.0.0","message":"Silahkan update","download_url":"https://github.com/irhagram/IME-Launcher/releases/latest"}}`))
	if st.Message != "Silahkan update" || st.DownloadURL == "" {
		t.Errorf("message/url lost: %+v", st)
	}
	st = decide("1.0.0", doc(t, `{"launcher":{"minimum_version":"2.0.0","download_url":"https://evil.example/app.exe"}}`))
	if st.DownloadURL != "" || st.Message == "" {
		t.Errorf("foreign host must be dropped and a default message used: %+v", st)
	}
	for _, u := range []string{"http://github.com/x", "https://github.com.evil.com/x", "javascript:alert(1)", ""} {
		if safeDownloadURL(u) != "" {
			t.Errorf("safeDownloadURL(%q) should be empty", u)
		}
	}
}

func TestFetchRemote(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"launcher":{"enabled":true,"latest_version":"1.0.0"}}`))
	}))
	defer ok.Close()
	if _, err := fetchRemote(context.Background(), ok.URL); err != nil {
		t.Errorf("valid doc: %v", err)
	}

	for name, h := range map[string]http.HandlerFunc{
		"404":         func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"bad json":    func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>`)) },
		"no launcher": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"x":1}`)) },
	} {
		srv := httptest.NewServer(h)
		if _, err := fetchRemote(context.Background(), srv.URL); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		srv.Close()
	}
}

func TestLockedBackend(t *testing.T) {
	a := NewApp() // starts in the "checking" (blocked) state
	if a.allowed() {
		t.Fatal("must start locked")
	}
	if r := a.Connect("main"); r.OK {
		t.Error("Connect must refuse while locked")
	}
	if err := a.StartDownload(); err == nil {
		t.Error("StartDownload must refuse while locked")
	}
	if r := a.TestGate("main"); r.OK {
		t.Error("TestGate must refuse while locked")
	}
	a.setStatus(LauncherStatus{State: "ok"})
	if !a.allowed() {
		t.Error("ok status must unlock")
	}
	a.setStatus(LauncherStatus{State: "force", Blocked: true})
	if a.allowed() {
		t.Error("force status must lock again")
	}
}

func TestFetchRemoteBOM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(append([]byte{0xEF, 0xBB, 0xBF}, `{"launcher":{"enabled":true,"latest_version":"1.0.0"}}`...))
	}))
	defer srv.Close()
	if _, err := fetchRemote(context.Background(), srv.URL); err != nil {
		t.Errorf("a UTF-8 BOM must be tolerated: %v", err)
	}
}
