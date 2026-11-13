package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestImportSettingsMergeAndReconnect(t *testing.T) {
	dir, exe := fakeKopia(t, "printf ok > \"$KOPIA_CONFIG_PATH\"")
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}, davs: []DAVConfig{{ID: "1111111111111111", Name: "existing", URL: "http://example.test", User: "u", Password: "original"}}}
	d := importDocument{Version: 1, DAVs: []exportDAV{{ID: "1111111111111111", Name: "duplicate", URL: "http://other.test", User: "u", Password: "wrong"}, {ID: "2222222222222222", Name: "new", URL: "http://nas.test/dav", User: "u"}}, Jobs: []exportJob{{ID: "3333333333333333", Name: "task", DAVID: "2222222222222222", Source: "/source", TargetPath: "repo", IntervalHours: 24}}}
	b, _ := json.Marshal(d)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", "token")
	f, _ := mw.CreateFormFile("settings", "settings.json")
	f.Write(b)
	mw.Close()
	req := httptest.NewRequest("POST", "/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	a.importSettings(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "预览导入设置") || strings.Contains(w.Body.String(), "wrong") {
		t.Fatal(w.Code, w.Body.String())
	}
	v := url.Values{"csrf": {"token"}, "dav_2222222222222222": {"dav-secret"}, "repo_3333333333333333": {"repository-secret"}}
	req = httptest.NewRequest("POST", "/import/apply", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	a.applyImport(w, req)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if len(a.davs) != 2 || len(a.jobs) != 1 || a.davs[0].Password != "original" || a.jobs[0].URL != "http://nas.test/dav/repo" || a.jobs[0].RepoPassword != "repository-secret" || !strings.Contains(a.message, "跳过 1 项") {
		t.Fatal("merge/reconnect failed", a.message)
	}
}

func TestImportFailurePreservesExistingSettings(t *testing.T) {
	dir, exe := fakeKopia(t, "echo no >&2; exit 1")
	a := &App{dir: dir, kopia: exe, csrf: "token", ctx: context.Background(), proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}, globalUploadKiB: 777, davs: []DAVConfig{{ID: "1111111111111111", Name: "existing", URL: "http://example.test", User: "u", Password: "keep"}}}
	a.pendingImport = &importDocument{Version: 1, GlobalUploadKiB: 100, DAVs: []exportDAV{{ID: "2222222222222222", Name: "new", URL: "http://nas.test/dav", User: "u", Password: "p"}}, Jobs: []exportJob{{ID: "3333333333333333", Name: "new", DAVID: "2222222222222222", Source: "/source", RepoPassword: "repository-secret"}}}
	v := url.Values{"csrf": {"token"}, "import_global_limit": {"yes"}}
	req := httptest.NewRequest("POST", "/import/apply", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.applyImport(w, req)
	a.wg.Wait()
	if w.Code != 303 || len(a.davs) != 1 || len(a.jobs) != 0 || a.globalUploadKiB != 777 || !strings.Contains(a.message, "原设置未更改") {
		t.Fatal("partial import", a.message)
	}
}
