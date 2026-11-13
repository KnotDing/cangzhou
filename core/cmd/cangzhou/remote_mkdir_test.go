package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateWebDAVRepositoryDirectories(t *testing.T) {
	dirs := map[string]bool{"/base": true, "/base/existing": true}
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "secret" {
			w.WriteHeader(401)
			return
		}
		if r.Method == "MKCOL" {
			if dirs[r.URL.Path] {
				w.WriteHeader(405)
				return
			}
			i := strings.LastIndex(r.URL.Path, "/")
			if !dirs[r.URL.Path[:i]] {
				w.WriteHeader(409)
				return
			}
			dirs[r.URL.Path] = true
			w.WriteHeader(201)
			return
		}
		if r.Method == "PROPFIND" && dirs[r.URL.Path] {
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	d := DAVConfig{URL: srv.URL + "/base", User: "user", Password: "secret"}
	if e := ensureRepositoryDirectory(context.Background(), d, "existing/新建/code"); e != nil {
		t.Fatal(e)
	}
	if !dirs["/base/existing/新建/code"] {
		t.Fatal("nested target missing")
	}
	if len(calls) != 6 || calls[0] != "PROPFIND /base" || calls[1] != "PROPFIND /base/existing" || calls[2] != "PROPFIND /base/existing/新建" || calls[3] != "MKCOL /base/existing/新建" {
		t.Fatal(calls)
	}
}
func TestConnectExistingDoesNotCreateDirectory(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer srv.Close()
	dir, exe := fakeKopia(t, "exit 0")
	a := &App{dir: dir, kopia: exe}
	cfg := Config{ID: "0123456789abcdef", URL: srv.URL + "/missing", RepoPassword: "test"}
	if _, e := a.prepareJob(cfg.ID); e != nil {
		t.Fatal(e)
	}
	d := DAVConfig{URL: srv.URL, User: "u", Password: "p"}
	if _, e := a.connectRemote(context.Background(), &cfg, d, "connect"); e != nil {
		t.Fatal(e)
	}
	if requests != 0 {
		t.Fatalf("connect attempted directory creation: %d", requests)
	}
}

func TestWebDAVSlashRedirectPreservesPropfind(t *testing.T) {
	seen := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/base" {
			http.Redirect(w, r, "/base/", 301)
			return
		}
		if r.Method == "PROPFIND" && r.URL.Path == "/base/" {
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`)
			return
		}
		w.WriteHeader(400)
	}))
	defer srv.Close()
	if e := ensureRepositoryDirectory(context.Background(), DAVConfig{URL: srv.URL + "/base", User: "u", Password: "p"}, ""); e != nil {
		t.Fatal(e)
	}
	if len(seen) != 2 || seen[1] != "PROPFIND /base/" {
		t.Fatal(seen)
	}
}

func TestWebDAVCreatesMissingBaseAndReportsDenied(t *testing.T) {
	dirs := map[string]bool{"/": true}
	deny := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			if !dirs[r.URL.Path] {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`)
			return
		}
		if r.Method == "MKCOL" {
			if deny {
				w.WriteHeader(403)
				return
			}
			dirs[r.URL.Path] = true
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(405)
	}))
	defer srv.Close()
	d := DAVConfig{URL: srv.URL + "/missing", User: "u", Password: "p"}
	if e := ensureRepositoryDirectory(context.Background(), d, "repo"); e != nil {
		t.Fatal(e)
	}
	if !dirs["/missing"] || !dirs["/missing/repo"] {
		t.Fatal("missing directories not created")
	}
	deny = true
	if e := ensureRepositoryDirectory(context.Background(), d, "denied"); e == nil || !strings.Contains(e.Error(), "403") {
		t.Fatalf("denied creation not reported: %v", e)
	}
}
