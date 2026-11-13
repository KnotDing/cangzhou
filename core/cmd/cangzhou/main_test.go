package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGatewayAuthorization(t *testing.T) {
	a := &App{csrf: "csrf-test"}
	for _, tc := range []struct {
		name      string
		peer      bool
		admin, id string
		want      int
	}{
		{"no peer", false, "true", "1000", 403}, {"nonadmin", true, "false", "1000", 403}, {"bad id", true, "true", "bad", 403}, {"admin", true, "true", "1000", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost/app/cangzhou/", nil)
			r = r.WithContext(context.WithValue(r.Context(), peerKey{}, tc.peer))
			r.Header.Set("X-Trim-Isadmin", tc.admin)
			r.Header.Set("X-Trim-Userid", tc.id)
			w := httptest.NewRecorder()
			a.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d", w.Code)
			}
		})
	}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), peerKey{}, true))
	r.Header.Add("X-Trim-Isadmin", "true")
	r.Header.Add("X-Trim-Isadmin", "true")
	r.Header.Set("X-Trim-Userid", "1000")
	w := httptest.NewRecorder()
	a.auth(func(http.ResponseWriter, *http.Request) { t.Fatal("accepted duplicate identity") })(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestCsrf(t *testing.T) {
	a := &App{csrf: "expected"}
	for _, tc := range []struct {
		csrf string
		ok   bool
	}{{"", false}, {"wrong", false}, {"expected", true}} {
		v := url.Values{"csrf": {tc.csrf}}
		r := httptest.NewRequest("POST", "/", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if (a.post(r) == nil) != tc.ok {
			t.Fatal(tc)
		}
	}
}

func TestSecurityPolicyAllowsDAVTestRequests(t *testing.T) {
	w := httptest.NewRecorder()
	securityHeaders(w)
	policy := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "connect-src 'self'") {
		t.Fatalf("same-origin connection test blocked: %s", policy)
	}
	if !strings.Contains(policy, "script-src 'self'") {
		t.Fatalf("same-origin script blocked: %s", policy)
	}
}
