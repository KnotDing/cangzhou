package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProgressParsing(t *testing.T) {
	p := backupProgress{JobID: "0123456789abcdef", Started: time.Now().Add(-time.Minute)}
	if !parseProgress(" | 1 hashing, 3 hashed (12.7 MB), 2 cached (28.9 GB), uploaded 0 B, estimating...", &p) || p.Processed != 28912700000 || p.KnownTotal {
		t.Fatalf("unexpected: %+v", p)
	}
	if !parseProgress(" | 0 hashing, 2 hashed (50 MB), 1 cached (25 MB), uploaded 1 MB, estimated 100 MB (75.0%) 3m12s left", &p) || !p.KnownTotal || p.Total != 100000000 || p.Remaining != "3m12s" {
		t.Fatalf("unexpected: %+v", p)
	}
	a := &App{progress: &p, jobs: []Config{{ID: p.JobID, Name: "demo", LastStatus: "running", RepoSizeKnown: true}}}
	w := httptest.NewRecorder()
	a.detail(w, httptest.NewRequest("GET", "/task?job="+p.JobID, nil))
	s := w.Body.String()
	for _, x := range []string{"备份进度", "已处理大小", "3m12s", "value=\"75\""} {
		if !strings.Contains(s, x) {
			t.Fatalf("missing %s", x)
		}
	}
	if parseProgress("corrupted line", &p) {
		t.Fatal("accepted invalid progress")
	}
	if !parseProgress("0 hashed (200 MB), 0 cached (0 B), uploaded 1 MB, estimated 100 MB (200%) 0s left", &p) || p.KnownTotal {
		t.Fatal("unreliable estimate should be hidden")
	}
}
