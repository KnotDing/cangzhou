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

func TestLocalStorageSubdirectories(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, base := filepath.Join(root, "source"), filepath.Join(root, "storage")
	for _, p := range []string{src, base} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{roots: []string{root}}
	d := DAVConfig{Protocol: "local", URL: (&url.URL{Scheme: "file", Path: base}).String()}
	target, err := localConnectionTarget(d, "nested/repo")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LocalPath: target, Source: src, TargetPath: "nested/repo"}
	if err := a.prepareLocalTarget(d, cfg, "connect"); err == nil {
		t.Fatal("connect created missing directory")
	}
	if _, err := os.Stat(filepath.Join(base, "nested")); !os.IsNotExist(err) {
		t.Fatal("connect created directory")
	}
	if err := a.prepareLocalTarget(d, cfg, "create"); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareLocalTarget(d, cfg, "connect"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../escape", "nested/../escape", "bad%2fpath"} {
		if _, err := localConnectionTarget(d, p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if err := os.Symlink(src, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	cfg.TargetPath = "link/repo"
	cfg.LocalPath = filepath.Join(base, "link", "repo")
	if err := a.prepareLocalTarget(d, cfg, "create"); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := os.Stat(filepath.Join(src, "repo")); !os.IsNotExist(err) {
		t.Fatal("created directory through symlink")
	}
	a.roots = []string{src}
	if err := a.prepareLocalTarget(d, cfg, "connect"); err == nil {
		t.Fatal("accepted revoked authorization")
	}
}

func TestSavedLocalStorageCreatesTask(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, base := filepath.Join(root, "source"), filepath.Join(root, "storage")
	for _, p := range []string{src, base} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, exe := fakeKopia(t, "exit 0")
	a := &App{dir: dir, kopia: exe, csrf: "token", roots: []string{root}, ctx: context.Background(), proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	v := url.Values{"name": {"local"}, "protocol": {"local"}, "local_path": {base}}
	if w := davPost(a, a.saveDAV, v); w.Code != 303 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if len(a.davs) != 1 || a.davs[0].Protocol != "local" {
		t.Fatal("local location not saved")
	}
	v = url.Values{"csrf": {"token"}, "name": {"backup"}, "source": {src}, "dav_id": {a.davs[0].ID}, "target_path": {"one/repo"}, "mode": {"create"}, "repo_password": {"test-password-123"}, "hours": {"0"}}
	r := httptest.NewRequest("POST", "/setup", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.setup(w, r)
	a.wg.Wait()
	if w.Code != 303 || len(a.jobs) != 1 || a.jobs[0].LocalPath != filepath.Join(base, "one", "repo") || a.jobs[0].DAVID != a.davs[0].ID {
		t.Fatalf("task: %d %s %s", w.Code, w.Body.String(), a.message)
	}
}

func TestUpdatingLocalStorageKeepsOriginalOnFailure(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, oldBase, newBase := filepath.Join(root, "source"), filepath.Join(root, "old"), filepath.Join(root, "new")
	for _, p := range []string{src, oldBase, newBase, filepath.Join(oldBase, "repo")} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, exe := fakeKopia(t, "exit 1")
	d := DAVConfig{ID: "1111111111111111", Name: "storage", Protocol: "local", URL: (&url.URL{Scheme: "file", Path: oldBase}).String()}
	job := Config{ID: "2222222222222222", Name: "job", DAVID: d.ID, Source: src, LocalPath: filepath.Join(oldBase, "repo"), URL: filepath.Join(oldBase, "repo"), TargetPath: "repo", RepoPassword: "test-password-123"}
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), roots: []string{root}, davs: []DAVConfig{d}, jobs: []Config{job}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	v := url.Values{"id": {d.ID}, "name": {"storage"}, "protocol": {"local"}, "local_path": {newBase}}
	if w := davPost(a, a.saveDAV, v); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if a.davs[0].URL != d.URL || a.jobs[0].LocalPath != job.LocalPath || !strings.Contains(a.message, "原配置未变") {
		t.Fatal("failed update changed original", a.message)
	}
	if _, err := os.Stat(filepath.Join(newBase, "repo")); !os.IsNotExist(err) {
		t.Fatal("update created missing existing repository")
	}
}
