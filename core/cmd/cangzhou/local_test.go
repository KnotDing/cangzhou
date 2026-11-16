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

func TestLocalRepositoryChecks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, repo := filepath.Join(root, "source"), filepath.Join(root, "repo")
	for _, p := range []string{src, repo} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{roots: []string{root}}
	if _, err := a.checkLocalRepository(repo, src); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{src, root, filepath.Join(root, "missing")} {
		if _, err := a.checkLocalRepository(p, src); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	a.roots = []string{src}
	if _, _, err := a.command(context.Background(), Config{ID: "0123456789abcdef", LocalPath: repo}, "snapshot", "list"); err == nil {
		t.Fatal("accepted revoked repository authorization")
	}
}

func TestLocalSetupAndReconnectFailure(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, repo := filepath.Join(root, "source"), filepath.Join(root, "repo")
	for _, p := range []string{src, repo} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, exe := fakeKopia(t, "exit 0")
	a := &App{dir: dir, kopia: exe, csrf: "token", roots: []string{root}, ctx: context.Background(), proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	v := url.Values{"csrf": {"token"}, "name": {"local"}, "source": {src}, "dav_id": {"local"}, "local_path": {repo}, "mode": {"create"}, "repo_password": {"test-password-123"}, "hours": {"0"}}
	request := func(path string, v url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		if path == "/setup" {
			a.setup(w, r)
		} else {
			a.reconnect(w, r)
		}
		return w
	}
	if w := request("/setup", v); w.Code != 303 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	a.wg.Wait()
	if len(a.jobs) != 1 || a.jobs[0].LocalPath != repo {
		t.Fatalf("job not saved: %#v", a.jobs)
	}
	old := a.jobs[0]
	v = url.Values{"csrf": {"token"}, "job": {old.ID}, "dav_id": {"local"}, "local_path": {src}, "mode": {"connect"}}
	if w := request("/reconnect", v); w.Code != 400 {
		t.Fatalf("overlap accepted: %d", w.Code)
	}
	if a.jobs[0].LocalPath != old.LocalPath {
		t.Fatal("failed reconnect changed job")
	}
}
