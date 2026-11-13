package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnverifiedDAVProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "remote" || p != "secret" {
			http.Error(w, "bad remote auth", 401)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/repository/") {
			http.Error(w, "bad path", 404)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	cfg := Config{URL: upstream.URL + "/repository/", User: "remote", WebDAVPassword: "secret"}
	p, local, e := newDAVProxy(&cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	resp, e := http.Get(local)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthorized: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", local, nil)
	req.SetBasicAuth(cfg.ProxyUser, cfg.ProxyPassword)
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("response %d %q", resp.StatusCode, body)
	}
	req, _ = http.NewRequest("GET", strings.Replace(local, "/repository/", "/other/", 1), nil)
	req.SetBasicAuth(cfg.ProxyUser, cfg.ProxyPassword)
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("path check: %d", resp.StatusCode)
	}
}
