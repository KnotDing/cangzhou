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

func TestRealKopiaLocalRepository(t *testing.T) {
	binary := os.Getenv("CANGZHOU_TEST_KOPIA")
	if binary == "" {
		t.Skip("requires CANGZHOU_TEST_KOPIA")
	}
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	src := filepath.Join(dir, "source")
	repo := filepath.Join(dir, "repository")
	if e := os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "a.txt"), []byte("test data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(src, "子目录"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "子目录", "nested.txt"), []byte("nested data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(src, "另一个"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "另一个", "second.txt"), []byte("second data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "not-selected.txt"), []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("a.txt", filepath.Join(src, "a-link")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dir, "restores"), 0700); e != nil {
		t.Fatal(e)
	}
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: binary, csrf: "token", roots: []string{dir}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "local", Source: src, LocalPath: repo, RepoPassword: "test-password-123", TrackingSince: time.Now()}}, proxyErrors: map[string]string{}}
	if _, e = a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if _, e = a.connectRemote(context.Background(), &a.jobs[0], DAVConfig{}, "create"); e != nil {
		t.Fatalf("create: %v", e)
	}
	// Reconnect through a separate configuration, then check that a wrong
	// password cannot replace that working connection.
	connected := a.jobs[0]
	connected.ConnectionID = random(8)
	p, e := a.repositoryConfigPath(connected)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = a.connectRemote(context.Background(), &connected, DAVConfig{}, "connect"); e != nil {
		t.Fatalf("connect existing: %v", e)
	}
	bad := connected
	bad.ConnectionID = random(8)
	bad.RepoPassword = "wrong-password-123"
	p, _ = a.repositoryConfigPath(bad)
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = a.connectRemote(context.Background(), &bad, DAVConfig{}, "connect"); e == nil {
		t.Fatal("wrong password accepted")
	}
	a.jobs[0] = connected
	if e = a.save(); e != nil {
		t.Fatal(e)
	}
	a.jobs = nil
	if e = a.loadJobs(); e != nil {
		t.Fatal(e)
	}
	if len(a.jobs) != 1 || a.jobs[0].LocalPath != repo {
		t.Fatal("local repository lost on restart")
	}
	if e = a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
	snapshots, e := a.list(j)
	if e != nil || len(snapshots) == 0 {
		t.Fatalf("list snapshots: %v", e)
	}
	root := snapshots[0].Root.Obj
	browse := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.browse(w, httptest.NewRequest("GET", "/browse?job="+id+"&id="+root+"&path="+url.QueryEscape(path), nil))
		return w
	}
	if w := browse(""); w.Code != 200 || !strings.Contains(w.Body.String(), "path=%E5%AD%90%E7%9B%AE%E5%BD%95") || strings.Contains(w.Body.String(), "nested.txt") {
		t.Fatalf("root browse: %d %s", w.Code, w.Body.String())
	}
	if w := browse("子目录"); w.Code != 200 || !strings.Contains(w.Body.String(), "nested.txt") {
		t.Fatalf("nested browse: %d %s", w.Code, w.Body.String())
	}
	if w := browse("../outside"); w.Code != 400 {
		t.Fatalf("traversal accepted: %d", w.Code)
	}
	selected := func(paths ...string) *httptest.ResponseRecorder {
		v := url.Values{"csrf": {"token"}, "job": {id}, "id": {root}, "selection": {"1"}, "selected": paths}
		r := httptest.NewRequest("POST", "/restore", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.restore(w, r)
		return w
	}
	if w := selected("../outside"); w.Code != 400 {
		t.Fatalf("accepted traversal: %d", w.Code)
	}
	if w := selected("not-present.txt"); w.Code != 400 {
		t.Fatalf("accepted nonexistent entry: %d", w.Code)
	}
	if w := selected("a-link"); w.Code != 400 {
		t.Fatalf("accepted symlink: %d", w.Code)
	}
	if w := selected("a.txt", "子目录", "子目录/nested.txt", "另一个/second.txt"); w.Code != 303 {
		t.Fatalf("selected restore: %d %s", w.Code, w.Body.String())
	}
	a.wg.Wait()
	restores := a.restores()
	if len(restores) != 1 {
		t.Fatalf("selected restore incomplete: %s", a.message)
	}
	restored := filepath.Join(dir, "restores", restores[0], "files")
	for path, want := range map[string]string{"a.txt": "test data", "子目录/nested.txt": "nested data", "另一个/second.txt": "second data"} {
		data, err := os.ReadFile(filepath.Join(restored, path))
		if err != nil || string(data) != want {
			t.Fatalf("restored %s: %q %v", path, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(restored, "not-selected.txt")); !os.IsNotExist(err) {
		t.Fatalf("unselected file restored: %v", err)
	}
	var live backupProgress
	_, progressErr := a.runWithProgress(context.Background(), j, func(line string) { parseProgress(line, &live) }, "snapshot", "create", "--json", "--progress", "--fail-fast", "--", src)
	if progressErr != nil || !live.KnownProcessed {
		t.Fatalf("actual Kopia progress unavailable: %v %+v", progressErr, live)
	}
	if j.RunCount != 1 || j.LastStatus != "success" {
		t.Fatalf("backup state: %+v", j)
	}
	if !j.RepoSizeKnown || j.RepoSizeBytes <= 0 || j.RepoSizeAt.IsZero() {
		t.Fatalf("backup did not automatically refresh repository size: %+v", j)
	}
	n, e := a.repositoryBytes(context.Background(), j)
	if e != nil || n <= 0 {
		t.Fatalf("repo bytes=%d err=%v", n, e)
	}
	if e = a.startSizeCheck(id, false); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	if !a.jobs[0].RepoSizeKnown || a.jobs[0].RepoSizeBytes <= 0 {
		t.Fatalf("size state: %+v", a.jobs[0])
	}
	b := &App{dir: dir}
	if e = b.loadJobs(); e != nil {
		t.Fatal(e)
	}
	if len(b.jobs) != 1 || b.jobs[0].RunCount != 1 || !b.jobs[0].RepoSizeKnown {
		t.Fatal("state not persisted")
	}
}
