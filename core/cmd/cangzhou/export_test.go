package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSettingsExportRedactsPasswordsByDefault(t *testing.T) {
	a := &App{csrf: "token", davs: []DAVConfig{{ID: "d", Name: "NAS", Password: "dav-secret", PrivateKey: "private-key-secret"}}, jobs: []Config{{ID: "j", Name: "资料", RepoPassword: "repo-secret", WebDAVPassword: "dav-secret", ProxyPassword: "proxy-secret"}}}
	for _, tc := range []struct{ include bool }{{false}, {true}} {
		v := url.Values{"csrf": {"token"}}
		if tc.include {
			v.Set("include_secrets", "yes")
		}
		r := httptest.NewRequest("POST", "/export/download", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.exportSettings(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(body, "资料") {
			t.Fatal(w.Code, body)
		}
		if strings.Contains(body, "proxy-secret") || strings.Contains(body, "private-key-secret") != tc.include || strings.Contains(body, "repo-secret") != tc.include || strings.Contains(body, "dav-secret") != tc.include {
			t.Fatal("incorrect secret export")
		}
	}
	r := httptest.NewRequest("POST", "/export/download", strings.NewReader("csrf=wrong&include_secrets=yes"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.exportSettings(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
