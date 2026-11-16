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

func TestCancelRunningBackupAndRetainHistory(t *testing.T) {
	dir, exe := fakeKopia(t, `echo ready > "$KOPIA_LOG_DIR/started"; exec sleep 30`)
	id, other := "0123456789abcdef", "abcdef0123456789"
	a := &App{dir: dir, kopia: exe, csrf: "token", roots: []string{dir}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "one", Source: dir, IntervalHours: 1, Next: time.Now().Add(-time.Hour)}, {ID: other, Name: "two", Source: dir}}}
	state, err := a.prepareJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startBackupWithTrigger(id, "scheduled"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.mu.Lock()
		if a.backupCancel != nil {
			a.backupCancel()
		}
		a.mu.Unlock()
		a.wg.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(state, "logs", "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.mu.Lock()
	run := a.backupRunID
	a.mu.Unlock()
	bad := url.Values{"csrf": {"invalid"}, "job": {id}, "run": {run}}
	r := httptest.NewRequest("POST", "/backup/cancel", strings.NewReader(bad.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rejected := httptest.NewRecorder()
	a.cancelBackup(rejected, r)
	if rejected.Code != 400 {
		t.Fatalf("invalid CSRF accepted: %d", rejected.Code)
	}
	w := httptest.NewRecorder()
	a.detail(w, httptest.NewRequest("GET", "/task?job="+id, nil))
	if !strings.Contains(w.Body.String(), "取消本次备份") || !strings.Contains(w.Body.String(), run) {
		t.Fatal("cancel control missing")
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {other}, "run": {run}})
	if w.Code != 409 {
		t.Fatalf("wrong job: %d", w.Code)
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {id}, "run": {"stale"}})
	if w.Code != 409 {
		t.Fatalf("stale run: %d", w.Code)
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {id}, "run": {run}})
	if w.Code != 303 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	a.wg.Wait()
	a.mu.Lock()
	job := a.jobs[0]
	busy := a.busy
	a.mu.Unlock()
	if busy || job.LastStatus != "cancelled" || len(job.History) != 1 || job.History[0].Status != "cancelled" || job.History[0].Finished.IsZero() || !job.Next.After(time.Now()) {
		t.Fatalf("wrong final state: %+v busy=%v", job, busy)
	}
	reloaded := &App{dir: dir}
	if err = reloaded.loadJobs(); err != nil {
		t.Fatal(err)
	}
	if reloaded.jobs[0].History[0].Status != "cancelled" {
		t.Fatal("cancelled history not persisted")
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {id}, "run": {run}})
	if w.Code != 409 {
		t.Fatalf("finished run: %d", w.Code)
	}
	if err = a.startBackup(id); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	nextRun := a.backupRunID
	a.mu.Unlock()
	if nextRun == run {
		t.Fatal("run token reused")
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {id}, "run": {run}})
	if w.Code != 409 {
		t.Fatalf("old run cancelled a new run: %d", w.Code)
	}
	w = davPost(a, a.cancelBackup, url.Values{"job": {id}, "run": {nextRun}})
	if w.Code != 303 {
		t.Fatalf("cancel second run: %d", w.Code)
	}
	a.wg.Wait()
}

func TestHomeHidesBackupWaitOnly(t *testing.T) {
	a := &App{csrf: "token", busy: true, activity: "执行备份：test", startedAt: time.Now().Add(-time.Minute), progress: &backupProgress{JobID: "0123456789abcdef"}}
	render := func() string {
		w := httptest.NewRecorder()
		a.home(w, httptest.NewRequest("GET", "/", nil))
		return w.Body.String()
	}
	if strings.Contains(render(), "已等待") {
		t.Fatal("backup wait text visible")
	}
	a.progress = nil
	a.activity = "统计仓库大小：test"
	if strings.Contains(render(), "已等待") {
		t.Fatal("repository size wait text visible")
	}
	a.activity = "连接仓库"
	if !strings.Contains(render(), "已等待") {
		t.Fatal("other operation wait text missing")
	}
}
