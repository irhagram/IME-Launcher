package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// loopbackOnly lets fetchFile talk to httptest servers for the duration of a test.
func loopbackOnly(t *testing.T) {
	t.Helper()
	old := trustedURL
	trustedURL = func(u *url.URL, _ bool) bool { return u.Scheme == "http" && u.Hostname() == "127.0.0.1" }
	t.Cleanup(func() { trustedURL = old })
}

func TestFetchFile(t *testing.T) {
	loopbackOnly(t)
	payload := []byte(strings.Repeat("MZ-new-launcher-", 5000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer srv.Close()
	noop := func(UpdateProgress) {}

	dst := filepath.Join(t.TempDir(), "x.new")
	if err := fetchFile(context.Background(), srv.URL, dst, sumOf(payload), noop); err != nil {
		t.Fatalf("valid download: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != string(payload) {
		t.Error("downloaded content differs")
	}

	bad := filepath.Join(t.TempDir(), "bad.new")
	err := fetchFile(context.Background(), srv.URL, bad, sumOf([]byte("something else")), noop)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("hash mismatch must fail, got %v", err)
	}
}

func TestFetchFileRefusals(t *testing.T) {
	noop := func(UpdateProgress) {}
	dst := filepath.Join(t.TempDir(), "x.new")

	// production rules: only https github.com
	if err := fetchFile(context.Background(), "https://evil.example/app.exe", dst, sumOf(nil), noop); err == nil {
		t.Error("foreign host must be refused")
	}
	if err := fetchFile(context.Background(), "http://github.com/a/b.exe", dst, sumOf(nil), noop); err == nil {
		t.Error("plain http must be refused")
	}

	loopbackOnly(t)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer evil.Close()
	// redirects to a host that is not trusted (localhost name is not 127.0.0.1)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(evil.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer redir.Close()
	if err := fetchFile(context.Background(), redir.URL, dst, sumOf([]byte("x")), noop); err == nil {
		t.Error("redirect to an untrusted host must fail")
	}

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	if err := fetchFile(context.Background(), missing.URL, dst, sumOf(nil), noop); err == nil {
		t.Error("HTTP 404 must fail")
	}
}

func TestGithubTrusted(t *testing.T) {
	ok := func(raw string, redirect bool) bool {
		u, _ := url.Parse(raw)
		return githubTrusted(u, redirect)
	}
	if !ok("https://github.com/o/r/releases/latest/download/a.exe", false) {
		t.Error("github.com must be trusted")
	}
	if ok("https://objects.githubusercontent.com/x", false) {
		t.Error("asset host must only be trusted as a redirect target")
	}
	if !ok("https://objects.githubusercontent.com/x", true) {
		t.Error("asset host must be trusted as a redirect target")
	}
	for _, bad := range []string{"https://githubusercontent.com.evil.com/x", "https://evilgithubusercontent.com/x", "http://github.com/x", "https://github.com.evil.com/x"} {
		if ok(bad, true) {
			t.Errorf("%s must not be trusted", bad)
		}
	}
}

func TestSwapExe(t *testing.T) {
	dir := t.TempDir()
	exe, nw, old := filepath.Join(dir, "L.exe"), filepath.Join(dir, "L.exe.new"), filepath.Join(dir, "L.exe.old")
	os.WriteFile(exe, []byte("v1"), 0o755)
	os.WriteFile(nw, []byte("v2"), 0o755)
	os.WriteFile(old, []byte("stale"), 0o755) // leftover from an earlier update

	if err := swapExe(exe, nw, old); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "v2" {
		t.Errorf("exe should now be v2, got %q", b)
	}
	if b, _ := os.ReadFile(old); string(b) != "v1" {
		t.Errorf(".old should hold v1, got %q", b)
	}
	if _, err := os.Stat(nw); err == nil {
		t.Error(".new should be gone")
	}
}

func TestSwapExeRollsBack(t *testing.T) {
	dir := t.TempDir()
	exe, old := filepath.Join(dir, "L.exe"), filepath.Join(dir, "L.exe.old")
	os.WriteFile(exe, []byte("v1"), 0o755)

	if err := swapExe(exe, filepath.Join(dir, "missing.new"), old); err == nil {
		t.Fatal("expected an error when the new file is missing")
	}
	if b, _ := os.ReadFile(exe); string(b) != "v1" {
		t.Errorf("original exe must be restored, got %q", b)
	}
}

func TestSelfUpdateFlag(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	u := `"download_url":"https://github.com/irhagram/IME-Launcher/releases/latest/download/IMELauncher.exe"`
	cases := []struct {
		name, js string
		want     bool
	}{
		{"sha + url", `{"launcher":{"latest_version":"2.0.0",` + u + `,"sha256":"` + sum + `"}}`, true},
		{"uppercase sha", `{"launcher":{"latest_version":"2.0.0",` + u + `,"sha256":"` + strings.ToUpper(sum) + `"}}`, true},
		{"no sha", `{"launcher":{"latest_version":"2.0.0",` + u + `}}`, false},
		{"short sha", `{"launcher":{"latest_version":"2.0.0",` + u + `,"sha256":"abc"}}`, false},
		{"no url", `{"launcher":{"latest_version":"2.0.0","sha256":"` + sum + `"}}`, false},
		{"foreign url", `{"launcher":{"latest_version":"2.0.0","download_url":"https://evil.example/a.exe","sha256":"` + sum + `"}}`, false},
		{"already current", `{"launcher":{"latest_version":"1.0.0",` + u + `,"sha256":"` + sum + `"}}`, false},
		{"maintenance", `{"launcher":{"maintenance":true,"latest_version":"2.0.0",` + u + `,"sha256":"` + sum + `"}}`, false},
	}
	for _, c := range cases {
		st := decide("1.0.0", doc(t, c.js))
		if st.SelfUpdate != c.want {
			t.Errorf("%s: SelfUpdate=%v, want %v", c.name, st.SelfUpdate, c.want)
		}
		if c.want && st.SHA256 != sum {
			t.Errorf("%s: sha must be normalised to lower case, got %q", c.name, st.SHA256)
		}
	}
}

func TestStartUpdateRefusedWithoutSelfUpdate(t *testing.T) {
	a := NewApp()
	a.setStatus(LauncherStatus{State: "update", DownloadURL: "https://github.com/x/y"})
	if err := a.StartUpdate(); err == nil {
		t.Error("StartUpdate must refuse when the status has no self-update")
	}
}
