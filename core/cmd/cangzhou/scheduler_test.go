package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDueTasksAreExecutedSequentially(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '{"id":"snapshot","rootEntry":{"summ":{"numFailed":0}}}'`)
	source := filepath.Join(dir, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	past := time.Now().Add(-time.Hour)
	a := &App{dir: dir, kopia: exe, roots: []string{source}, ctx: context.Background(), jobs: []Config{{ID: "0123456789abcdef", Name: "one", Source: source, IntervalHours: 1, Next: past}, {ID: "abcdef0123456789", Name: "two", Source: source, IntervalHours: 2, Next: past}}, proxyErrors: map[string]string{}}
	for _, j := range a.jobs {
		if _, e := a.prepareJob(j.ID); e != nil {
			t.Fatal(e)
		}
	}
	a.scheduleOnce()
	a.wg.Wait()
	if !a.jobs[0].Next.After(time.Now()) || !a.jobs[1].Next.Equal(past) {
		t.Fatalf("first tick: %+v", a.jobs)
	}
	a.scheduleOnce()
	a.wg.Wait()
	if !a.jobs[1].Next.After(time.Now()) {
		t.Fatalf("second tick: %+v", a.jobs)
	}
}

func TestSnapshotClickDuringBackupShowsBusyState(t *testing.T) {
	dir, exe := fakeKopia(t, `sleep 1; printf '{"id":"snapshot","rootEntry":{"summ":{"numFailed":0}}}'`)
	source := filepath.Join(dir, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, roots: []string{source}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "照片", Source: source}}, proxyErrors: map[string]string{}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if e := a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	a.snapshots(w, httptest.NewRequest("GET", "/snapshots?job="+id, nil))
	if w.Code != 303 || w.Header().Get("Location") != base+"/" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	a.mu.Lock()
	activity := a.activity
	a.mu.Unlock()
	if !strings.Contains(activity, "照片") {
		t.Fatalf("activity %q", activity)
	}
	a.wg.Wait()
}
