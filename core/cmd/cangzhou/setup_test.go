package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeKopia(t *testing.T, script string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "kopia")
	if e := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return dir, p
}
func setupRequest() *httptest.ResponseRecorder { return httptest.NewRecorder() }
func submitSetup(a *App, w *httptest.ResponseRecorder) {
	f := url.Values{"csrf": {"token"}, "dav_id": {"abcdef0123456789"}, "repo_password": {"repo-secret-1234"}, "mode": {"connect"}, "source": {a.dir}, "hours": {"24"}, "target_path": {"nas-a/cangzhou"}, "name": {"工作资料"}}
	r := httptest.NewRequest("POST", "/setup", strings.NewReader(f.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.setup(w, r)
}
func TestSetupProgressAndDuplicate(t *testing.T) {
	dir, exe := fakeKopia(t, "sleep 1\nexit 0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: ctx, roots: []string{dir}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "测试", URL: "http://example.test/dav/", User: "tester", Password: "dav-secret"}}}
	w := setupRequest()
	submitSetup(a, w)
	if w.Code != 303 {
		t.Fatalf("setup=%d %s", w.Code, w.Body.String())
	}
	a.mu.Lock()
	busy, activity := a.busy, a.activity
	a.mu.Unlock()
	if !busy || activity != "连接仓库" {
		t.Fatalf("busy=%v activity=%q", busy, activity)
	}
	second := setupRequest()
	submitSetup(a, second)
	if second.Code != 303 {
		t.Fatalf("duplicate=%d", second.Code)
	}
	snap := httptest.NewRecorder()
	a.snapshots(snap, httptest.NewRequest("GET", "/snapshots", nil))
	if snap.Code != 303 || snap.Header().Get("Location") != base+"/" {
		t.Fatalf("snapshot=%d %v", snap.Code, snap.Header())
	}
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not finish")
	}
	canonicalDir, _ := filepath.EvalSymlinks(dir)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.jobs) != 1 {
		t.Fatalf("jobs=%d", len(a.jobs))
	}
	j := a.jobs[0]
	if a.busy || j.URL != "http://example.test/dav/nas-a/cangzhou" || j.Source != canonicalDir || j.IntervalHours != 24 || j.Next.IsZero() || !strings.Contains(a.message, "已创建") {
		t.Fatalf("busy=%v url=%s message=%s", a.busy, j.URL, a.message)
	}
	if _, e := os.Stat(filepath.Join(dir, "state.json")); e != nil {
		t.Fatal(e)
	}
}
func TestSetupFailureReleasesBusyAndHidesSecrets(t *testing.T) {
	dir, exe := fakeKopia(t, "echo 'bad repo-secret-1234 dav-secret' >&2\nexit 1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: ctx, roots: []string{dir}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "测试", URL: "http://example.test/dav/", User: "tester", Password: "dav-secret"}}}
	w := setupRequest()
	submitSetup(a, w)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy || len(a.jobs) != 0 || !strings.Contains(a.message, "连接失败") {
		t.Fatalf("bad failed state: busy=%v message=%q", a.busy, a.message)
	}
	if strings.Contains(a.message, "repo-secret-1234") || strings.Contains(a.message, "dav-secret") {
		t.Fatal("secret leaked")
	}
}

func TestRunDoesNotWaitForInheritedOutput(t *testing.T) {
	dir, exe := fakeKopia(t, "(sleep 4) &\nprintf 'done'")
	a := &App{dir: dir, kopia: exe}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := a.prepareJob("legacy"); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	out, err := a.run(ctx, Config{ID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "done" {
		t.Fatalf("output %q", out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited on inherited output: %v", time.Since(start))
	}
}

func TestRepositoryURL(t *testing.T) {
	u, _ := url.Parse("http://example.test/dav/base/")
	for _, tc := range []struct {
		path, want string
		fail       bool
	}{
		{"", "http://example.test/dav/base/", false}, {"机器/backup", "http://example.test/dav/base/%E6%9C%BA%E5%99%A8/backup", false},
		{"../escape", "", true}, {"a//b", "", true}, {"a/%2e%2e", "", true}, {"a\\b", "", true},
	} {
		got, e := repositoryURL(u, tc.path)
		if (e != nil) != tc.fail || (!tc.fail && got != tc.want) {
			t.Fatalf("%q: %q %v", tc.path, got, e)
		}
	}
}

func TestMissingRepositoryDirectoryIsExplained(t *testing.T) {
	dir, exe := fakeKopia(t, "echo 'error reading WebDAV dir: PROPFIND /: 404' >&2\nexit 1")
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), roots: []string{dir}, davs: []DAVConfig{{ID: "abcdef0123456789", Name: "目标", URL: "http://example.test/dav/", User: "tester", Password: "dav-secret"}}}
	submitSetup(a, setupRequest())
	a.wg.Wait()
	if !strings.Contains(a.message, "确认账号有访问及创建目录的权限") || !strings.Contains(a.message, "http://example.test/dav/nas-a/cangzhou") {
		t.Fatalf("unexpected error: %s", a.message)
	}
}
