package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHomeShowsJobsAndNewFormWithoutSecrets(t *testing.T) {
	a := &App{dir: t.TempDir(), csrf: "testtoken", roots: []string{"/shared"}, jobs: []Config{{ID: "0123456789abcdef", Name: "Photos", Source: "/shared/photos", URL: "http://nas/dav/photos", RepoPassword: "REPO_SECRET", WebDAVPassword: "DAV_SECRET"}, {ID: "abcdef0123456789", Name: "Documents", Source: "/shared/docs", URL: "http://nas/dav/docs"}}}
	for _, tc := range []struct {
		path string
		new  bool
	}{{"/", false}, {"/?new=1", true}} {
		w := httptest.NewRecorder()
		a.home(w, httptest.NewRequest("GET", tc.path, nil))
		s := w.Body.String()
		for _, x := range []string{"Photos", "Documents", "http://nas/dav/photos", "/shared/docs"} {
			if !strings.Contains(s, x) {
				t.Fatalf("%s missing %q", tc.path, x)
			}
		}
		if strings.Contains(s, "REPO_SECRET") || strings.Contains(s, "DAV_SECRET") {
			t.Fatal("secret exposed")
		}
		newpos := strings.Index(s, "新建备份任务")
		taskpos := strings.Index(s, "Photos")
		if tc.new && (newpos < 0 || newpos > taskpos) {
			t.Fatalf("new task form should precede tasks")
		}
		if !tc.new && newpos >= 0 {
			t.Fatal("unexpected new task form")
		}
	}
}

func TestTaskListSearchAndDetails(t *testing.T) {
	j := Config{ID: "0123456789abcdef", Name: "照片", URL: "http://nas/dav", Source: "/shared/photos", User: "admin", RepoPassword: "REPO_SECRET", WebDAVPassword: "DAV_SECRET", RepoSizeKnown: true, RepoSizeBytes: 2048, RepoSizeAt: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC), TrackingSince: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC), RunCount: 3}
	a := &App{dir: t.TempDir(), csrf: "token", roots: []string{"/shared"}, jobs: []Config{j, {ID: "abcdef0123456789", Name: "文档", URL: "http://nas/docs", Source: "/shared/docs"}}, proxyErrors: map[string]string{}}
	w := httptest.NewRecorder()
	a.home(w, httptest.NewRequest("GET", "/?q=照片", nil))
	s := w.Body.String()
	if !strings.Contains(s, "照片") || strings.Contains(s, "文档") || !strings.Contains(s, "<table>") {
		t.Fatal("search or table invalid")
	}
	w = httptest.NewRecorder()
	a.detail(w, httptest.NewRequest("GET", "/task?job="+j.ID, nil))
	s = w.Body.String()
	for _, x := range []string{"照片", "2.00 KiB", "运行次数", ">3<", "上一次运行"} {
		if !strings.Contains(s, x) {
			t.Fatalf("detail missing %q", x)
		}
	}
	if strings.Contains(s, "REPO_SECRET") || strings.Contains(s, "DAV_SECRET") {
		t.Fatal("detail exposes secrets")
	}
	w = httptest.NewRecorder()
	a.edit(w, httptest.NewRequest("GET", "/edit?job="+j.ID, nil))
	s = w.Body.String()
	if !strings.Contains(s, "目标仓库连接") || strings.Contains(s, "REPO_SECRET") || strings.Contains(s, "DAV_SECRET") {
		t.Fatal("edit page invalid or exposes secrets")
	}
}
