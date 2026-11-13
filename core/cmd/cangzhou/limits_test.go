package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLimitsApplyAndPersist(t *testing.T) {
	for _, tc := range []struct{ g, j, w int }{{0, 0, 0}, {100, 0, 100}, {0, 200, 200}, {100, 200, 100}, {200, 100, 100}} {
		if effectiveLimit(tc.g, tc.j) != tc.w {
			t.Fatal(tc)
		}
	}
	dir := t.TempDir()
	j := Config{ID: "0123456789abcdef", Name: "x", UploadLimitKiB: 200}
	a := &App{dir: dir, csrf: "token", jobs: []Config{j}}
	jd, e := a.prepareJob(j.ID)
	if e != nil {
		t.Fatal(e)
	}
	original := []byte(`{"storage":{"type":"webdav","config":{"url":"http://localhost"}}}`)
	if e = os.WriteFile(filepath.Join(jd, "repository.config"), original, 0600); e != nil {
		t.Fatal(e)
	}
	p, e := a.limitedRepositoryConfig(j, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(p)
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var cfg struct {
		Storage struct{ Config map[string]any }
	}
	if e = json.Unmarshal(b, &cfg); e != nil || cfg.Storage.Config["maxUploadSpeedBytesPerSecond"] != float64(102400) {
		t.Fatal(string(b), e)
	}
	b, _ = os.ReadFile(filepath.Join(jd, "repository.config"))
	if string(b) != string(original) {
		t.Fatal("modified original config")
	}
	v := url.Values{"csrf": {"token"}, "upload_limit_kib": {"100"}}
	r := httptest.NewRequest("POST", "/limits/save", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.saveLimits(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	reload := &App{dir: dir}
	if e = reload.loadJobs(); e != nil || reload.globalUploadKiB != 100 || reload.jobs[0].UploadLimitKiB != 200 {
		t.Fatal(e)
	}
}
