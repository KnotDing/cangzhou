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

func pauseReq(a *App, id, action string) int {
	v := url.Values{"csrf": {"token"}, "job": {id}, "action": {action}}
	r := httptest.NewRequest("POST", "/pause", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.pause(w, r)
	return w.Code
}
func TestPausedTaskDoesNotStartAndResumeReschedules(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '{"id":"snapshot","rootEntry":{"summ":{"numFailed":0}}}'`)
	src := filepath.Join(dir, "src")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	id := "0123456789abcdef"
	past := time.Now().Add(-time.Hour)
	a := &App{dir: dir, kopia: exe, csrf: "token", roots: []string{src}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "x", Source: src, IntervalHours: 2, Next: past}}, proxyErrors: map[string]string{}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if n := pauseReq(a, id, "pause"); n != 303 {
		t.Fatal(n)
	}
	a.scheduleOnce()
	if a.busy || a.jobs[0].RunCount != 0 {
		t.Fatal("paused task ran")
	}
	if a.startBackup(id) == nil {
		t.Fatal("manual run allowed while paused")
	}
	if n := pauseReq(a, id, "resume"); n != 303 {
		t.Fatal(n)
	}
	if a.jobs[0].Paused || !a.jobs[0].Next.After(time.Now().Add(time.Hour)) {
		t.Fatalf("%+v", a.jobs[0])
	}
	if e := a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
	if j.RunCount != 1 || j.LastStatus != "success" || j.LastStarted.IsZero() || j.LastFinished.IsZero() {
		t.Fatalf("%+v", j)
	}
}
func TestPauseWhileRunningDoesNotCancel(t *testing.T) {
	dir, exe := fakeKopia(t, `sleep 1; printf '{"id":"snapshot","rootEntry":{"summ":{"numFailed":0}}}'`)
	src := filepath.Join(dir, "src")
	_ = os.Mkdir(src, 0700)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, csrf: "token", roots: []string{src}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "x", Source: src}}, proxyErrors: map[string]string{}}
	_, _ = a.prepareJob(id)
	if e := a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	if n := pauseReq(a, id, "pause"); n != 303 {
		t.Fatal(n)
	}
	a.wg.Wait()
	if !a.jobs[0].Paused || a.jobs[0].LastStatus != "success" {
		t.Fatalf("%+v", a.jobs[0])
	}
}

func TestPausedJobDoesNotBlockOtherDueJob(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '{"id":"snapshot","rootEntry":{"summ":{"numFailed":0}}}'`)
	src := filepath.Join(dir, "src")
	_ = os.Mkdir(src, 0700)
	past := time.Now().Add(-time.Hour)
	a := &App{dir: dir, kopia: exe, roots: []string{src}, ctx: context.Background(), jobs: []Config{{ID: "0123456789abcdef", Name: "paused", Source: src, Paused: true, IntervalHours: 1, Next: past}, {ID: "abcdef0123456789", Name: "active", Source: src, IntervalHours: 1, Next: past}}, proxyErrors: map[string]string{}}
	for _, j := range a.jobs {
		if _, e := a.prepareJob(j.ID); e != nil {
			t.Fatal(e)
		}
	}
	a.scheduleOnce()
	a.wg.Wait()
	if a.jobs[0].RunCount != 0 || a.jobs[1].RunCount != 1 || a.jobs[1].LastStatus != "success" {
		t.Fatalf("%+v", a.jobs)
	}
}

func TestFailedBackupIsRecorded(t *testing.T) {
	dir, exe := fakeKopia(t, "echo rejected >&2; exit 1")
	src := filepath.Join(dir, "src")
	_ = os.Mkdir(src, 0700)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, roots: []string{src}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "failed", Source: src}}, proxyErrors: map[string]string{}}
	_, _ = a.prepareJob(id)
	if e := a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
	if j.RunCount != 1 || j.LastStatus != "failed" || j.LastError == "" || j.LastFinished.IsZero() || j.TrackingSince.IsZero() {
		t.Fatalf("%+v", j)
	}
}
