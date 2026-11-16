package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackupHistoryLifecycle(t *testing.T) {
	dir, exe := fakeKopia(t, `if [ "$1" = "snapshot" ]; then echo '0 hashed (0 B), 0 cached (0 B), uploaded 2 MB' >&2; printf '{"id":"snapshot-id"}'; else printf '[{"length":200}]'; fi`)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, roots: []string{dir}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "照片", Source: dir}}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if e := a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	if e := a.startBackupWithTrigger(id, "scheduled"); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	records := a.jobs[0].History
	if len(records) != 2 {
		t.Fatalf("records: %+v", records)
	}
	for i, trigger := range []string{"manual", "scheduled"} {
		r := records[i]
		if r.Status != "success" || r.Trigger != trigger || r.Started.IsZero() || r.Finished.IsZero() || !r.UploadedKnown || r.Uploaded != 2000000 || !r.RepoSizeKnown || r.RepoSizeBytes != 200 {
			t.Fatalf("record: %+v", r)
		}
	}
	b := &App{dir: dir}
	if e := b.loadJobs(); e != nil {
		t.Fatal(e)
	}
	if len(b.jobs) != 1 || len(b.jobs[0].History) != 2 || b.jobs[0].History[1].Trigger != "scheduled" {
		t.Fatalf("restored: %+v", b.jobs)
	}
	w := httptest.NewRecorder()
	b.backupHistory(w, httptest.NewRequest(http.MethodGet, "/history?job="+id, nil))
	for _, want := range []string{"任务开始时间", "任务耗时", "1.91 MiB", "计划", "手动", "/app/cangzhou/task?job=" + id} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %q in %s", want, w.Body.String())
		}
	}
}

func TestHistoryInterruptedAndUnknown(t *testing.T) {
	dir := t.TempDir()
	id := "0123456789abcdef"
	a := &App{dir: dir, jobs: []Config{{ID: id, Name: "test", LastStatus: "running", History: []backupRecord{{Started: time.Now().Add(-time.Minute), Status: "running", Trigger: "manual"}}}}}
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	b := &App{dir: dir}
	if err := b.loadJobs(); err != nil {
		t.Fatal(err)
	}
	if b.jobs[0].History[0].Status != "interrupted" || !b.jobs[0].History[0].Finished.IsZero() {
		t.Fatalf("record %+v", b.jobs[0].History[0])
	}
	w := httptest.NewRecorder()
	b.backupHistory(w, httptest.NewRequest("GET", "/history?job="+id, nil))
	if !strings.Contains(w.Body.String(), "未知") || !strings.Contains(w.Body.String(), "状态未确认") {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	b.backupHistory(w, httptest.NewRequest("GET", "/history?job=invalid", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestFailedBackupHistoryDoesNotReuseOldSize(t *testing.T) {
	dir, exe := fakeKopia(t, `echo 'backup failed' >&2; exit 1`)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, roots: []string{dir}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "test", Source: dir, RepoSizeKnown: true, RepoSizeBytes: 42}}}
	if _, err := a.prepareJob(id); err != nil {
		t.Fatal(err)
	}
	if err := a.startBackup(id); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	r := a.jobs[0].History[0]
	if r.Status != "failed" || r.Error == "" || r.RepoSizeKnown || r.UploadedKnown || r.Finished.IsZero() {
		t.Fatalf("record %+v", r)
	}
}
