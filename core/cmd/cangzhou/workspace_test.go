package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePages(t *testing.T) {
	dir := t.TempDir()
	complete := filepath.Join(dir, "restores", "restore-123")
	pending := filepath.Join(dir, "restores", "restore-456")
	for _, p := range []string{filepath.Join(complete, "files"), filepath.Join(pending, "files")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(complete, ".complete"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{dir: dir, roots: []string{"/granted/shared"}}
	for _, tc := range []struct {
		path   string
		fn     http.HandlerFunc
		want   []string
		absent []string
	}{
		{"recoveries", a.recoveries, []string{"已恢复文件", "restore-123", "/app/cangzhou/download?id=restore-123"}, []string{"restore-456", "/granted/shared"}},
		{"permissions", a.permissions, []string{"已授权目录", "/granted/shared"}, []string{"restore-123"}},
		{"tips", a.tips, []string{"单独保存每个仓库的密码", "定期测试历史快照恢复"}, []string{"restore-123", "/granted/shared"}},
	} {
		w := httptest.NewRecorder()
		tc.fn(w, httptest.NewRequest("GET", "/"+tc.path, nil))
		s := w.Body.String()
		if w.Code != 200 {
			t.Fatal(tc.path, w.Code)
		}
		for _, x := range tc.want {
			if !strings.Contains(s, x) {
				t.Fatalf("%s missing %q", tc.path, x)
			}
		}
		for _, x := range tc.absent {
			if strings.Contains(s, x) {
				t.Fatalf("%s unexpectedly contains %q", tc.path, x)
			}
		}
		if !strings.Contains(s, `class="nav-item active" href="/app/cangzhou/`+tc.path+`"`) {
			t.Fatalf("wrong active navigation for %s", tc.path)
		}
		w = httptest.NewRecorder()
		tc.fn(w, httptest.NewRequest("POST", "/"+tc.path, nil))
		if w.Code != 405 {
			t.Fatal(tc.path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.home(w, httptest.NewRequest("GET", "/", nil))
	s := w.Body.String()
	for _, x := range []string{"<h2 class=\"card-title\">已恢复文件", "<h2 class=\"card-title\">已授权目录", "restore-123"} {
		if strings.Contains(s, x) {
			t.Fatalf("home still contains %s", x)
		}
	}
}
