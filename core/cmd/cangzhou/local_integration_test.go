package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRealKopiaLocalRepository(t *testing.T) {
	binary := os.Getenv("CANGZHOU_TEST_KOPIA")
	if binary == "" {
		t.Skip("requires CANGZHOU_TEST_KOPIA")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	repo := filepath.Join(dir, "repository")
	if e := os.Mkdir(src, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "a.txt"), []byte("test data"), 0600); e != nil {
		t.Fatal(e)
	}
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: binary, roots: []string{src}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "local", Source: src, RepoPassword: "test-password-123", TrackingSince: time.Now()}}, proxyErrors: map[string]string{}}
	state, e := a.prepareJob(id)
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"repository", "create", "filesystem", "--path=" + repo, "--no-check-for-updates"}
	if runtime.GOOS == "darwin" {
		args = append(args, "--no-use-keychain")
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "KOPIA_PASSWORD=test-password-123", "KOPIA_CONFIG_PATH="+filepath.Join(state, "repository.config"), "KOPIA_LOG_DIR="+filepath.Join(state, "logs"), "KOPIA_CACHE_DIRECTORY="+filepath.Join(state, "cache"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("create: %v %s", e, out)
	}
	if e = a.startBackup(id); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
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
