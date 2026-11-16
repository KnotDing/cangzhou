package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveRefreshRegionsOnBusyPages(t *testing.T) {
	j := Config{ID: "0123456789abcdef", Name: "test", RepoSizeKnown: true}
	a := &App{dir: t.TempDir(), csrf: "token", busy: true, activity: "执行备份", startedAt: time.Now(), jobs: []Config{j}, progress: &backupProgress{JobID: j.ID, Started: time.Now()}}
	for _, tc := range []struct {
		name, path string
		handler    func(*httptest.ResponseRecorder)
		regions    []string
	}{
		{"home", "/?new=1", func(w *httptest.ResponseRecorder) { a.home(w, httptest.NewRequest("GET", "/?new=1", nil)) }, []string{"live-home-status", "live-home-message", "live-home-jobs", "live-setup-fieldset", "live-setup-submit"}},
		{"detail", "/task", func(w *httptest.ResponseRecorder) { a.detail(w, httptest.NewRequest("GET", "/task?job="+j.ID, nil)) }, []string{"live-detail-status", "live-detail-message", "live-detail-progress", "live-detail-summary"}},
		{"remote", "/webdav", func(w *httptest.ResponseRecorder) { a.webdav(w, httptest.NewRequest("GET", "/webdav?new=1", nil)) }, []string{"live-remote-message", "live-remote-save", "live-remote-list"}},
		{"offline", "/offline", func(w *httptest.ResponseRecorder) { a.offlinePage(w, httptest.NewRequest("GET", "/offline", nil)) }, []string{"live-offline-message", "live-offline-local", "live-offline-upload"}},
		{"recoveries", "/recoveries", func(w *httptest.ResponseRecorder) { a.recoveries(w, httptest.NewRequest("GET", "/recoveries", nil)) }, []string{"live-recoveries-message", "live-recoveries-list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w)
			s := w.Body.String()
			if w.Code != 200 || !strings.Contains(s, `data-live-busy="true"`) || !strings.Contains(s, "/assets/live-refresh.js") || strings.Contains(s, `http-equiv="refresh"`) {
				t.Fatalf("page cannot refresh locally: %d", w.Code)
			}
			for _, id := range tc.regions {
				if !strings.Contains(s, `id="`+id+`"`) {
					t.Fatalf("missing %s", id)
				}
			}
		})
	}
	a.busy = false
	a.progress = nil
	w := httptest.NewRecorder()
	a.detail(w, httptest.NewRequest("GET", "/task?job="+j.ID, nil))
	if !strings.Contains(w.Body.String(), `id="live-detail-progress"`) || !strings.Contains(w.Body.String(), `data-live-busy="false"`) {
		t.Fatal("idle detail lacks update target")
	}
}

func TestRemoteFormRemainsEditableDuringBackup(t *testing.T) {
	a := &App{dir: t.TempDir(), csrf: "token", busy: true, activity: "执行备份", startedAt: time.Now()}
	render := func() string {
		w := httptest.NewRecorder()
		a.webdav(w, httptest.NewRequest("GET", "/webdav?new=1", nil))
		if w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
		return w.Body.String()
	}
	busy := render()
	if strings.Contains(busy, `<fieldset disabled`) || strings.Contains(busy, `id="live-remote-fieldset"`) || !strings.Contains(busy, `id="live-remote-save" data-live-disabled type="submit" disabled`) || !strings.Contains(busy, `id="dav-test" type="button"`) {
		t.Fatal("busy form should allow editing and testing while preventing save")
	}
	a.busy = false
	idle := render()
	if !strings.Contains(idle, `id="live-remote-save" data-live-disabled type="submit" >`) {
		t.Fatal("save should become available when idle")
	}
}
