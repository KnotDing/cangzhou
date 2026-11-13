package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationPreservesLegacyConfiguration(t *testing.T) {
	dir := t.TempDir()
	next := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	old := Config{URL: "http://nas.example/dav", User: "u", WebDAVPassword: "webdav-secret", RepoPassword: "repo-secret", Source: "/data", IntervalHours: 24, Next: next}
	data, _ := json.Marshal(old)
	if e := os.WriteFile(filepath.Join(dir, "job.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "repository.config"), []byte("legacy-repository"), 0600); e != nil {
		t.Fatal(e)
	}
	a := &App{dir: dir}
	if e := a.loadJobs(); e != nil {
		t.Fatal(e)
	}
	if len(a.jobs) != 1 {
		t.Fatalf("got %d jobs", len(a.jobs))
	}
	j := a.jobs[0]
	if j.ID != "legacy" || j.Source != old.Source || j.URL != old.URL || j.RepoPassword != old.RepoPassword || !j.Next.Equal(next) {
		t.Fatalf("bad migration: %+v", j)
	}
	for _, p := range []string{"job.json", "repository.config", "jobs/legacy/repository.config", "state.json"} {
		if _, e := os.Stat(filepath.Join(dir, p)); e != nil {
			t.Fatalf("%s: %v", p, e)
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "jobs/legacy/repository.config"))
	if e != nil || string(b) != "legacy-repository" {
		t.Fatalf("repository copy %q %v", b, e)
	}
	a2 := &App{dir: dir}
	if e = a2.loadJobs(); e != nil || len(a2.jobs) != 1 || a2.jobs[0].ID != "legacy" {
		t.Fatalf("reload %v %+v", e, a2.jobs)
	}
}
func TestMigrationMissingRepositoryFailsWithoutDeletingLegacy(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "job.json"), []byte(`{"RepoPassword":"secret","Source":"/data"}`), 0600); e != nil {
		t.Fatal(e)
	}
	a := &App{dir: dir}
	if a.loadJobs() == nil {
		t.Fatal("expected failure")
	}
	if _, e := os.Stat(filepath.Join(dir, "job.json")); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(e) {
		t.Fatalf("unexpected jobs file: %v", e)
	}
}
func TestInvalidJobID(t *testing.T) {
	a := &App{dir: t.TempDir()}
	for _, s := range []string{"../escape", "", "other", "123/456"} {
		if _, e := a.jobDir(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestInterruptedRunIsReportedAsUnknown(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir, jobs: []Config{{ID: "0123456789abcdef", Name: "unfinished", LastStatus: "running", RunCount: 2}}}
	if _, e := a.prepareJob(a.jobs[0].ID); e != nil {
		t.Fatal(e)
	}
	if e := a.save(); e != nil {
		t.Fatal(e)
	}
	b := &App{dir: dir}
	if e := b.loadJobs(); e != nil {
		t.Fatal(e)
	}
	if b.jobs[0].LastStatus != "interrupted" || b.jobs[0].LastError == "" || b.jobs[0].RunCount != 2 {
		t.Fatalf("%+v", b.jobs[0])
	}
}
