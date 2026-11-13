package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeZip(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "repo.zip")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	w, e := z.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte("data"))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	return p
}
func TestZipRejectsEscapeAndFindsRepository(t *testing.T) {
	dest := t.TempDir()
	if unzipRepository(makeZip(t, "../escape"), dest) == nil {
		t.Fatal("accepted traversal")
	}
	dest = t.TempDir()
	if e := unzipRepository(makeZip(t, "repo/kopia.repository.f"), dest); e != nil {
		t.Fatal(e)
	}
	root, e := locateRepository(dest)
	if e != nil || root != filepath.Join(dest, "repo") {
		t.Fatal(root, e)
	}
}

func TestOfflineRepositoryRealKopia(t *testing.T) {
	binary := os.Getenv("CANGZHOU_TEST_KOPIA")
	if binary == "" {
		t.Skip("requires CANGZHOU_TEST_KOPIA")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repository")
	data := filepath.Join(root, "data")
	os.MkdirAll(source, 0700)
	os.MkdirAll(data, 0700)
	os.MkdirAll(filepath.Join(data, "restores"), 0700)
	os.WriteFile(filepath.Join(source, "a.txt"), []byte("offline test"), 0600)
	cfgPath := filepath.Join(root, "config")
	env := append(os.Environ(), "KOPIA_PASSWORD=test-password-123", "KOPIA_CONFIG_PATH="+cfgPath, "KOPIA_LOG_DIR="+filepath.Join(root, "logs"), "KOPIA_CACHE_DIRECTORY="+filepath.Join(root, "cache"))
	run := func(args ...string) {
		t.Helper()
		c := exec.Command(binary, args...)
		c.Env = env
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("kopia: %v %s", e, b)
		}
	}
	args := []string{"repository", "create", "filesystem", "--path=" + repo, "--no-check-for-updates"}
	if runtime.GOOS == "darwin" {
		args = append(args, "--no-use-keychain")
	}
	run(args...)
	run("snapshot", "create", "--no-progress", "--", source)
	a := &App{dir: data, kopia: binary, roots: []string{root}, csrf: "token", ctx: context.Background(), proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
	open := func(mode, p string) {
		t.Helper()
		v := url.Values{"csrf": {"token"}, "mode": {mode}, "path": {p}, "password": {"test-password-123"}}
		r := httptest.NewRequest("POST", "/offline/open", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.offlineOpen(w, r)
		if w.Code != 303 || a.offline == nil || len(a.offline.Snapshots) == 0 {
			t.Fatalf("%s %d %s", mode, w.Code, w.Body.String())
		}
	}
	closeRepo := func() {
		t.Helper()
		v := url.Values{"csrf": {"token"}}
		r := httptest.NewRequest("POST", "/offline/close", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.offlineClose(w, r)
		if w.Code != 303 {
			t.Fatal(w.Code)
		}
	}
	open("directory", repo)
	id := a.offline.Snapshots[0].Root.Obj
	v := url.Values{"csrf": {"token"}, "id": {id}}
	r := httptest.NewRequest("POST", "/offline/restore", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.offlineRestore(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if len(a.restores()) != 1 {
		t.Fatal("restore not completed", a.message)
	}
	closeRepo()
	archive := filepath.Join(root, "repo.zip")
	f, e := os.Create(archive)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	e = filepath.Walk(repo, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(repo, p)
		if e != nil {
			return e
		}
		w, e := z.Create("repo/" + filepath.ToSlash(rel))
		if e != nil {
			return e
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		_, e = io.Copy(w, in)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	open("archive", archive)
	closeRepo()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", "token")
	mw.WriteField("password", "test-password-123")
	part, e := mw.CreateFormFile("archive", "repo.zip")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(archive)
	if e != nil {
		t.Fatal(e)
	}
	part.Write(b)
	mw.Close()
	r = httptest.NewRequest("POST", "/offline/open", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	a.offlineOpen(w, r)
	if w.Code != 303 || a.offline == nil || len(a.offline.Snapshots) == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	closeRepo()
}

func TestLocalRepositoryRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if e := os.Symlink("/tmp", filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	if validateLocalRepository(root) == nil {
		t.Fatal("accepted symlink")
	}
}
