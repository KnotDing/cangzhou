package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSnapshotNavigation(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '[]'`)
	const id = "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, ctx: context.Background(), jobs: []Config{{ID: id, Name: "照片", RepoSizeKnown: true}}}
	if _, err := a.prepareJob(id); err != nil {
		t.Fatal(err)
	}

	detail := httptest.NewRecorder()
	a.detail(detail, httptest.NewRequest(http.MethodGet, base+"/task?job="+id, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `href="/app/cangzhou/snapshots?job=`+id+`"`) {
		t.Fatalf("detail status %d, body %s", detail.Code, detail.Body.String())
	}
	// A fresh request also models opening the snapshots URL directly, with no history or referrer.
	snapshots := httptest.NewRecorder()
	a.snapshots(snapshots, httptest.NewRequest(http.MethodGet, base+"/snapshots?job="+id, nil))
	if snapshots.Code != http.StatusOK || !strings.Contains(snapshots.Body.String(), `href="/app/cangzhou/task?job=`+id+`">← 返回任务详情</a>`) {
		t.Fatalf("snapshots status %d, body %s", snapshots.Code, snapshots.Body.String())
	}
	for _, query := range []string{"", "?job=ffffffffffffffff"} {
		w := httptest.NewRecorder()
		a.snapshots(w, httptest.NewRequest(http.MethodGet, base+"/snapshots"+query, nil))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/" {
			t.Fatalf("query %q: status %d, location %q", query, w.Code, w.Header().Get("Location"))
		}
	}
}
