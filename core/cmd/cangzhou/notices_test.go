package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNotificationsStayWithRelatedPageAndTask(t *testing.T) {
	j1 := Config{ID: "0123456789abcdef", Name: "first", RepoSizeKnown: true}
	j2 := Config{ID: "abcdef0123456789", Name: "second", RepoSizeKnown: true}
	a := &App{dir: t.TempDir(), csrf: "token", jobs: []Config{j1, j2}, proxyErrors: map[string]string{}}
	get := func(fn func(*httptest.ResponseRecorder)) string {
		t.Helper()
		w := httptest.NewRecorder()
		fn(w)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		return w.Body.String()
	}
	home := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.home(w, httptest.NewRequest("GET", "/", nil)) })
	}
	detail := func(id string) string {
		return get(func(w *httptest.ResponseRecorder) { a.detail(w, httptest.NewRequest("GET", "/task?job="+id, nil)) })
	}
	remote := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.webdav(w, httptest.NewRequest("GET", "/webdav", nil)) })
	}
	recovery := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.recoveries(w, httptest.NewRequest("GET", "/recoveries", nil)) })
	}
	limits := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.limitsPage(w, httptest.NewRequest("GET", "/limits", nil)) })
	}
	exports := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.exportPage(w, httptest.NewRequest("GET", "/export", nil)) })
	}
	offline := func() string {
		return get(func(w *httptest.ResponseRecorder) { a.offlinePage(w, httptest.NewRequest("GET", "/offline", nil)) })
	}
	if _, e := a.acquire("执行备份", j1.ID); e != nil {
		t.Fatal(e)
	}
	a.release("BACKUP_ERROR_MARKER")
	if !strings.Contains(home(), "BACKUP_ERROR_MARKER") || !strings.Contains(detail(j1.ID), "BACKUP_ERROR_MARKER") {
		t.Fatal("backup error hidden from task")
	}
	for _, page := range []string{detail(j2.ID), remote(), recovery(), limits(), exports(), offline()} {
		if strings.Contains(page, "BACKUP_ERROR_MARKER") {
			t.Fatal("backup error leaked to unrelated page")
		}
	}
	if _, e := a.acquire("更新 WebDAV 配置", ""); e != nil {
		t.Fatal(e)
	}
	a.release("REMOTE_ERROR_MARKER")
	if !strings.Contains(remote(), "REMOTE_ERROR_MARKER") {
		t.Fatal("remote error not shown")
	}
	for _, page := range []string{home(), recovery(), limits(), exports()} {
		if strings.Contains(page, "REMOTE_ERROR_MARKER") {
			t.Fatal("remote error leaked")
		}
	}
	if _, e := a.acquire("恢复离线快照", ""); e != nil {
		t.Fatal(e)
	}
	a.release("RESTORE_ERROR_MARKER")
	if !strings.Contains(recovery(), "RESTORE_ERROR_MARKER") {
		t.Fatal("restore error not shown")
	}
	for _, page := range []string{home(), remote(), limits(), offline()} {
		if strings.Contains(page, "RESTORE_ERROR_MARKER") {
			t.Fatal("restore error leaked")
		}
	}
	form := url.Values{"csrf": {"token"}, "upload_limit_kib": {"128"}}
	limitReq := httptest.NewRequest("POST", "/limits/save", strings.NewReader(form.Encode()))
	limitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	limitWriter := httptest.NewRecorder()
	a.saveLimits(limitWriter, limitReq)
	if limitWriter.Code != 303 || !strings.Contains(limits(), "全局备份上传限速已保存") {
		t.Fatal("limit save notice missing")
	}
	for _, page := range []string{home(), remote(), recovery(), exports()} {
		if strings.Contains(page, "全局备份上传限速已保存") {
			t.Fatal("limits notice leaked")
		}
	}
	if _, e := a.acquire("导入设置", ""); e != nil {
		t.Fatal(e)
	}
	a.release("IMPORT_ERROR_MARKER")
	if !strings.Contains(exports(), "IMPORT_ERROR_MARKER") {
		t.Fatal("import error not shown")
	}
	for _, page := range []string{home(), remote(), recovery(), limits()} {
		if strings.Contains(page, "IMPORT_ERROR_MARKER") {
			t.Fatal("import error leaked")
		}
	}
}

func TestWorkspaceIconsAreAccessibleAndDistinct(t *testing.T) {
	html := navHeader("webdav")
	for _, name := range []string{"overview", "recovery", "permissions", "tips", "remote", "limit", "export"} {
		if !strings.Contains(html, `class="nav-icon icon-`+name+`" aria-hidden="true"`) {
			t.Fatalf("missing icon %s", name)
		}
	}
	if strings.Count(html, "<svg ") != 7 || !strings.Contains(html, `class="nav-item active" href="/app/cangzhou/webdav"`) {
		t.Fatal("workspace icons or active navigation incorrect")
	}
}
