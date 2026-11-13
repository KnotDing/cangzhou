package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reconnectReq(a *App, id string) *httptest.ResponseRecorder {
	v := url.Values{"csrf": {"token"}, "job": {id}, "dav_id": {"abcdef0123456789"}, "mode": {"connect"}}
	r := httptest.NewRequest("POST", "/reconnect", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.reconnect(w, r)
	return w
}
func TestReconnectFailurePreservesOldConnection(t *testing.T) {
	dir, exe := fakeKopia(t, "echo failure >&2\nexit 1")
	id := "0123456789abcdef"
	old := Config{ID: id, Name: "x", URL: "http://old.test/dav", User: "olduser", WebDAVPassword: "oldwebdav", RepoPassword: "oldpassword123", Source: "/source"}
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), jobs: []Config{old}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "新连接", URL: "http://new.test/webdav", User: "newuser", Password: "newpassword"}}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	d, e := a.prepareJob(id)
	if e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(d, "repository.config")
	if e = os.WriteFile(original, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	w := reconnectReq(a, id)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	j := a.jobs[0]
	if j.URL != old.URL || j.User != old.User || j.ConnectionID != "" || !strings.Contains(a.message, "原连接仍保留") {
		t.Fatalf("%+v %s", j, a.message)
	}
	b, e := os.ReadFile(original)
	if e != nil || string(b) != "original" {
		t.Fatal(string(b), e)
	}
}
func TestReconnectSuccessAtomicallySelectsNewConfig(t *testing.T) {
	dir, exe := fakeKopia(t, "printf '%s' ok > \"$KOPIA_CONFIG_PATH\"\nexit 0")
	id := "0123456789abcdef"
	old := Config{ID: id, Name: "x", URL: "http://old.test/dav", User: "olduser", WebDAVPassword: "oldwebdav", RepoPassword: "oldpassword123", Source: "/source", RunCount: 3, RepoSizeKnown: true, RepoSizeBytes: 99}
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), jobs: []Config{old}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "新连接", URL: "http://new.test/webdav", User: "newuser", Password: "newpassword"}}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	d, e := a.prepareJob(id)
	if e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(d, "repository.config")
	if e = os.WriteFile(original, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	w := reconnectReq(a, id)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	j := a.jobs[0]
	if j.URL != "http://new.test/webdav" || j.ConnectionID == "" || j.RepoSizeKnown || j.RunCount != 3 || j.WebDAVPassword != "newpassword" {
		t.Fatalf("%+v", j)
	}
	p, e := a.repositoryConfigPath(j)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "ok" {
		t.Fatal(string(b), e)
	}
	b, e = os.ReadFile(original)
	if e != nil || string(b) != "original" {
		t.Fatal(string(b), e)
	}
}

func TestPauseDuringReconnectIsPreserved(t *testing.T) {
	dir, exe := fakeKopia(t, "sleep 1\nprintf '%s' ok > \"$KOPIA_CONFIG_PATH\"")
	id := "0123456789abcdef"
	old := Config{ID: id, Name: "x", URL: "http://old.test/dav", User: "olduser", WebDAVPassword: "oldwebdav", RepoPassword: "oldpassword123", Source: "/source", IntervalHours: 24}
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), jobs: []Config{old}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "新连接", URL: "http://new.test/webdav", User: "newuser", Password: "newpassword"}}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	w := reconnectReq(a, id)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	if status := pauseReq(a, id, "pause"); status != 303 {
		t.Fatal(status)
	}
	a.wg.Wait()
	if !a.jobs[0].Paused || a.jobs[0].URL != "http://new.test/webdav" {
		t.Fatalf("pause or target lost: %+v", a.jobs[0])
	}
}
