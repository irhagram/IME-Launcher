package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const relJSON = `{"tag_name":"1.0.3","name":"IME Launcher v1.0.3","body":"## Yang baru\r\n- satu\r\n- dua","html_url":"https://github.com/irhagram/IME-Launcher/releases/tag/1.0.3","published_at":"2026-11-04T10:00:00Z"}`

func TestFetchNotes(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/releases/tags/1.0.3":
			w.Write([]byte(relJSON))
		case "/releases/latest":
			w.Write([]byte(strings.Replace(relJSON, `"1.0.3"`, `"1.0.9"`, 1)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n, err := fetchNotes(context.Background(), srv.URL+"/releases", "v1.0.3")
	if err != nil || !n.OK || n.Tag != "1.0.3" || n.Title != "IME Launcher v1.0.3" {
		t.Fatalf("tag lookup failed: %+v %v", n, err)
	}
	if strings.Contains(n.Body, "\r") || !strings.HasPrefix(n.Body, "## Yang baru\n- satu") {
		t.Errorf("body not normalised: %q", n.Body)
	}
	if n.URL == "" || n.Published == 0 {
		t.Errorf("url/date lost: %+v", n)
	}

	// unknown tag falls back to the latest release
	n, err = fetchNotes(context.Background(), srv.URL+"/releases", "7.7.7")
	if err != nil || n.Tag != "1.0.9" {
		t.Errorf("fallback to latest failed: %+v %v", n, err)
	}
}

func TestFetchNotesErrors(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := fetchNotes(context.Background(), notFound.URL, "1.0.3"); err == nil {
		t.Error("404 everywhere must be an error")
	}
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"message":"rate limit"}`)) }))
	defer junk.Close()
	if _, err := fetchNotes(context.Background(), junk.URL, "1.0.3"); err == nil {
		t.Error("a response without tag_name is not a release")
	}
}

func TestReleaseURLMustBeGithub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Replace(relJSON, "https://github.com/irhagram", "https://evil.example/irhagram", 1)))
	}))
	defer srv.Close()
	n, err := fetchRelease(context.Background(), srv.URL)
	if err != nil || n.URL != "" {
		t.Errorf("non-github release link must be dropped: %+v %v", n, err)
	}
}
