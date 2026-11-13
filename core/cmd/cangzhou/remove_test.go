package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveOnlyUnregistersSelectedTask(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir, csrf: "token", jobs: []Config{{ID: "legacy", Name: "old"}, {ID: "0123456789abcdef", Name: "other"}}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	jobdir, e := a.prepareJob("legacy")
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(jobdir, "repository.config")
	if e = os.WriteFile(p, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	f := url.Values{"csrf": {"token"}, "job": {"legacy"}, "confirm": {"yes"}}
	r := httptest.NewRequest("POST", "/remove", strings.NewReader(f.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.remove(w, r)
	if w.Code != 303 || len(a.jobs) != 1 || a.jobs[0].ID != "0123456789abcdef" {
		t.Fatalf("status %d jobs %+v", w.Code, a.jobs)
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "keep" {
		t.Fatalf("local repository settings changed: %s %v", b, e)
	}
	if _, e = os.Stat(filepath.Join(dir, "state.json")); e != nil {
		t.Fatal(e)
	}
}
func TestRemoveRequiresConfirmation(t *testing.T) {
	a := &App{dir: t.TempDir(), csrf: "token", jobs: []Config{{ID: "legacy", Name: "old"}}}
	f := url.Values{"csrf": {"token"}, "job": {"legacy"}}
	r := httptest.NewRequest("POST", "/remove", strings.NewReader(f.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.remove(w, r)
	if w.Code != 400 || len(a.jobs) != 1 {
		t.Fatal(w.Code, a.jobs)
	}
}
