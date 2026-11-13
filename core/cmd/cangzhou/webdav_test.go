package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func davPost(a *App, handler func(http.ResponseWriter, *http.Request), v url.Values) *httptest.ResponseRecorder {
	v.Set("csrf", "token")
	r := httptest.NewRequest("POST", "/", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}
func TestDAVTestBeforeSave(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "0" {
			t.Errorf("unexpected request %s", r.Method)
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "secret" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(207)
		_, _ = w.Write([]byte(`<d:multistatus xmlns:d="DAV:"></d:multistatus>`))
	}))
	defer srv.Close()
	a := &App{dir: t.TempDir(), csrf: "token"}
	v := url.Values{"name": {"目标"}, "url": {srv.URL}, "user": {"user"}, "password": {"secret"}}
	test := func(want bool) {
		t.Helper()
		w := davPost(a, a.testDAV, v)
		var x struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil || x.OK != want {
			t.Fatalf("response %s %v", w.Body.String(), err)
		}
	}
	test(false)
	v.Set("insecure_tls", "1")
	test(true)
	a.davs = []DAVConfig{{ID: "abcdef0123456789", Name: "目标", URL: srv.URL, User: "user", Password: "secret", InsecureTLS: true}}
	v.Set("id", "abcdef0123456789")
	v.Del("password")
	test(true)
	v.Set("password", "wrong")
	test(false)
	if calls != 3 {
		t.Fatalf("unexpected successful TLS requests %d", calls)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("test saved state: %v", err)
	}
}
func TestDAVSyncAllOrNothing(t *testing.T) {
	dir, exe := fakeKopia(t, `case "$KOPIA_CONFIG_PATH" in *abcdef0123456789*) echo failure >&2; exit 1;; esac
printf ok > "$KOPIA_CONFIG_PATH"`)
	endpoint := DAVConfig{ID: "1111111111111111", Name: "目标", URL: "http://old.test/dav", User: "old", Password: "oldsecret"}
	jobs := []Config{{ID: "0123456789abcdef", Name: "一", DAVID: endpoint.ID, TargetPath: "one", URL: "http://old.test/dav/one", RepoPassword: "password123"}, {ID: "abcdef0123456789", Name: "二", DAVID: endpoint.ID, TargetPath: "two", URL: "http://old.test/dav/two", RepoPassword: "password123"}}
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), davs: []DAVConfig{endpoint}, jobs: jobs, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	for _, j := range jobs {
		if _, err := a.prepareJob(j.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"id": {endpoint.ID}, "name": {"目标"}, "url": {"http://new.test/dav"}, "user": {"new"}, "password": {"newsecret"}}
	if w := davPost(a, a.saveDAV, form); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if a.davs[0].URL != endpoint.URL || a.jobs[0].URL != jobs[0].URL || a.jobs[1].URL != jobs[1].URL || !strings.Contains(a.message, "原配置未变") {
		t.Fatalf("partial update: %s", a.message)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf ok > \"$KOPIA_CONFIG_PATH\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if w := davPost(a, a.saveDAV, form); w.Code != 303 {
		t.Fatal(w.Code)
	}
	a.wg.Wait()
	if a.davs[0].URL != "http://new.test/dav" || a.jobs[0].URL != "http://new.test/dav/one" || a.jobs[1].URL != "http://new.test/dav/two" || a.jobs[0].WebDAVPassword != "newsecret" {
		t.Fatalf("update failed: %s", a.message)
	}
	b := &App{dir: dir}
	if err := b.loadJobs(); err != nil || len(b.davs) != 1 || len(b.jobs) != 2 || b.jobs[1].URL != a.jobs[1].URL {
		t.Fatalf("reload failed: %v", err)
	}
	rm := url.Values{"id": {endpoint.ID}, "confirm": {"yes"}}
	davPost(a, a.removeDAV, rm)
	if len(a.davs) != 1 {
		t.Fatal("removed in-use connection")
	}
}
